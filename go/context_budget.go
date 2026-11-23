package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	maxUpstreamFrameText = 60 * 1024 // 60KB safe headroom under 64KB frame limit
	maxSystemPromptCap   = 24 * 1024 // 24KB system prompt cap
	truncMarker          = "\n…[truncated]"
)

func fitStringBytes(s string, maxBytes int) string {
	b := []byte(s)
	if len(b) <= maxBytes {
		return s
	}
	if maxBytes <= len(truncMarker) {
		return string(b[:maxBytes])
	}
	return string(b[:maxBytes-len(truncMarker)]) + truncMarker
}

type PreparedTurn struct {
	Text           string
	SystemPrompt   string
	Model          string
	ThinkingEffort string
	Tools          []ClientTool
	HasTools       bool
}

func prepareTurnContext(payload []byte, reqModel string) PreparedTurn {
	pt := PreparedTurn{
		Model:          "smart",
		ThinkingEffort: "off",
	}
	if reqModel != "" {
		pt.Model = reqModel
	}

	var reqObj struct {
		Model          string          `json:"model"`
		System         any             `json:"system"`
		ReasoningEffort string         `json:"reasoning_effort"`
		ThinkingEffort string          `json:"thinking_effort"`
		Messages       []struct {
			Role       string          `json:"role"`
			Content    any             `json:"content"`
			Name       string          `json:"name,omitempty"`
			ToolCallID string          `json:"tool_call_id,omitempty"`
			ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
		} `json:"messages"`
		Tools []ClientTool `json:"tools"`
	}

	if json.Unmarshal(payload, &reqObj) != nil {
		pt.Text = fitStringBytes(string(payload), maxUpstreamFrameText)
		pt.Model = mapModelToQuickTier(pt.Model)
		return pt
	}

	if strings.TrimSpace(reqObj.Model) != "" {
		pt.Model = strings.TrimSpace(reqObj.Model)
	}
	pt.Model = mapModelToQuickTier(pt.Model)
	pt.Tools = reqObj.Tools
	pt.HasTools = len(pt.Tools) > 0

	effort := reqObj.ReasoningEffort
	if effort == "" {
		effort = reqObj.ThinkingEffort
	}
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high", "max":
		pt.ThinkingEffort = strings.ToLower(strings.TrimSpace(effort))
	case "minimal":
		pt.ThinkingEffort = "low"
	case "med":
		pt.ThinkingEffort = "medium"
	case "xhigh":
		pt.ThinkingEffort = "max"
	case "true", "enabled":
		pt.ThinkingEffort = "high"
	default:
		pt.ThinkingEffort = "off"
	}

	// 1. Extract and cap System Prompt
	var sysParts []string
	if reqObj.System != nil {
		if s, ok := reqObj.System.(string); ok && strings.TrimSpace(s) != "" {
			sysParts = append(sysParts, strings.TrimSpace(s))
		}
	}
	for _, m := range reqObj.Messages {
		if m.Role == "system" {
			txt := extractMessageText(m.Content)
			if txt != "" {
				sysParts = append(sysParts, txt)
			}
		}
	}
	if len(sysParts) > 0 {
		pt.SystemPrompt = fitStringBytes(strings.Join(sysParts, "\n\n"), maxSystemPromptCap)
	}

	// 2. Build Tool Preamble if tools are present
	preamble := ""
	if pt.HasTools {
		preamble = buildToolPreamble(pt.Tools)
	}

	// 3. Assemble and budget conversation messages
	// History messages formatted from newest to oldest within text budget
	availBudget := maxUpstreamFrameText - len([]byte(pt.SystemPrompt)) - len([]byte(preamble)) - 1024
	if availBudget < 4096 {
		// If tools preamble was too big, drop parameter schemas or reduce
		availBudget = 4096
	}

	var formattedMsgs []string
	for _, m := range reqObj.Messages {
		if m.Role == "system" {
			continue
		}
		txt := extractMessageText(m.Content)
		if m.Role == "assistant" {
			if len(m.ToolCalls) > 0 && string(m.ToolCalls) != "null" {
				txt += "\n[called tools: " + string(m.ToolCalls) + "]"
			}
			formattedMsgs = append(formattedMsgs, "Assistant: "+txt)
		} else if m.Role == "tool" || m.Role == "function" {
			label := m.ToolCallID
			if label == "" {
				label = m.Name
			}
			formattedMsgs = append(formattedMsgs, fmt.Sprintf("[tool_result %s]\n%s", label, txt))
		} else {
			formattedMsgs = append(formattedMsgs, txt)
		}
	}

	// Fit formatted messages into availBudget from newest backwards
	var kept []string
	used := 0
	for i := len(formattedMsgs) - 1; i >= 0; i-- {
		msgBytes := len([]byte(formattedMsgs[i])) + 2
		if used+msgBytes > availBudget {
			if len(kept) == 0 {
				// Even the latest message alone is huge, fit it
				kept = append(kept, fitStringBytes(formattedMsgs[i], availBudget))
			}
			break
		}
		kept = append([]string{formattedMsgs[i]}, kept...)
		used += msgBytes
	}

	convoText := strings.Join(kept, "\n\n")
	if preamble != "" {
		pt.Text = preamble + "\n\n" + convoText
	} else {
		pt.Text = convoText
	}
	if strings.TrimSpace(pt.Text) == "" {
		pt.Text = "Hello"
	}

	return pt
}

func extractMessageText(content any) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, elem := range v {
			if em, ok := elem.(map[string]any); ok {
				if em["type"] == "text" {
					if ts, ok := em["text"].(string); ok {
						parts = append(parts, ts)
					}
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	default:
		return ""
	}
}
