package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type FunctionDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type ClientTool struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

type ParsedToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func buildToolPreamble(tools []ClientTool) string {
	if len(tools) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Client tool calling]\n")
	b.WriteString("The application you are assisting exposes the tools below; it executes them on the user's machine and returns each result as a [tool_result …] message.\n")
	b.WriteString("This channel has no native function-calling API — the <tool_call> marker below is the only way to invoke these tools.\n")
	b.WriteString("When the task needs one of them, reply with ONLY a block in this format (no other text):\n")
	b.WriteString("<tool_call>{\"name\":\"<tool_name>\",\"arguments\":{<json arguments>}}</tool_call>\n\n")
	b.WriteString("Guidelines:\n")
	b.WriteString("- one block per call; several blocks are allowed for parallel calls;\n")
	b.WriteString("- \"arguments\" is a JSON object matching the parameters schema;\n")
	b.WriteString("- after a [tool_result …] message arrives, continue the task in plain text;\n\n")
	b.WriteString("Tools provided by the client:\n")

	for _, t := range tools {
		paramsJSON, _ := json.Marshal(t.Function.Parameters)
		desc := t.Function.Description
		if desc == "" {
			desc = "(no description)"
		}
		b.WriteString(fmt.Sprintf("- %s: %s\n  parameters: %s\n", t.Function.Name, desc, string(paramsJSON)))
	}
	return b.String()
}

type ToolCallParser struct {
	buf      string
	inBlock  bool
	blockBuf string
	calls    []ParsedToolCall
	index    int
}

func NewToolCallParser() *ToolCallParser {
	return &ToolCallParser{}
}

func (p *ToolCallParser) Push(delta string) (visibleText string, newCalls []ParsedToolCall) {
	if delta == "" {
		return "", nil
	}
	p.buf += delta
	openTag := "<tool_call>"
	closeTag := "</tool_call>"

	for {
		if !p.inBlock {
			idx := strings.Index(p.buf, openTag)
			if idx == -1 {
				hold := 0
				for k := len(openTag) - 1; k >= 1; k-- {
					if strings.HasSuffix(p.buf, openTag[:k]) {
						hold = k
						break
					}
				}
				visibleText += p.buf[:len(p.buf)-hold]
				p.buf = p.buf[len(p.buf)-hold:]
				break
			}
			visibleText += p.buf[:idx]
			p.buf = p.buf[idx+len(openTag):]
			p.inBlock = true
			p.blockBuf = ""
			continue
		}

		idx := strings.Index(p.buf, closeTag)
		if idx == -1 {
			hold := 0
			for k := len(closeTag) - 1; k >= 1; k-- {
				if strings.HasSuffix(p.buf, closeTag[:k]) {
					hold = k
					break
				}
			}
			p.blockBuf += p.buf[:len(p.buf)-hold]
			p.buf = p.buf[len(p.buf)-hold:]
			break
		}

		p.blockBuf += p.buf[:idx]
		p.buf = p.buf[idx+len(closeTag):]
		p.inBlock = false

		var rawCall struct {
			Name      string `json:"name"`
			Arguments any    `json:"arguments"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(p.blockBuf)), &rawCall) == nil && rawCall.Name != "" {
			argStr := ""
			switch a := rawCall.Arguments.(type) {
			case string:
				argStr = a
			case map[string]any:
				b, _ := json.Marshal(a)
				argStr = string(b)
			default:
				argStr = "{}"
			}
			tc := ParsedToolCall{
				Index: p.index,
				ID:    fmt.Sprintf("call_%s_%d", genUUID()[:8], p.index+1),
				Type:  "function",
			}
			tc.Function.Name = rawCall.Name
			tc.Function.Arguments = argStr
			p.index++
			p.calls = append(p.calls, tc)
			newCalls = append(newCalls, tc)
		}
		p.blockBuf = ""
	}
	return visibleText, newCalls
}

func (p *ToolCallParser) End() (visibleText string, newCalls []ParsedToolCall) {
	if !p.inBlock && len(p.buf) > 0 {
		visibleText = p.buf
		p.buf = ""
	}
	return visibleText, nil
}
