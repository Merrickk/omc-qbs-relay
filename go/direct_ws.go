package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	quickUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) AWSQuick/1.2.4660 Chrome/150.0.7871.250 Electron/43.8.7 Safari/537.36"
)

func genUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func parseEventStream(data []byte) [][]byte {
	var frames [][]byte
	off, n := 0, len(data)
	for off+16 <= n {
		total := int(binary.BigEndian.Uint32(data[off : off+4]))
		hlen := int(binary.BigEndian.Uint32(data[off+4 : off+8]))
		if total < 16 || off+total > n {
			break
		}
		hend := off + 12 + hlen
		payload := data[hend : off+total-4]
		frames = append(frames, payload)
		off += total
	}
	return frames
}

func wsEnvelope(obj any) []byte {
	b, _ := json.Marshal(obj)
	raw := map[string]any{
		"eventType": "rawInputEvent",
		"payload": map[string]string{
			"payload": base64.StdEncoding.EncodeToString(b),
		},
	}
	out, _ := json.Marshal(raw)
	return out
}

func parseUserPrompt(payload []byte, reqModel string) (prompt string, model string) {
	model = "smart"
	if reqModel != "" {
		model = reqModel
	}
	var body struct {
		Model    string `json:"model"`
		System   any    `json:"system"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(payload, &body) == nil {
		if strings.TrimSpace(body.Model) != "" {
			model = strings.TrimSpace(body.Model)
		}
		var parts []string
		if body.System != nil {
			if s, ok := body.System.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, "[System Instructions]\n"+strings.TrimSpace(s))
			}
		}
		for _, m := range body.Messages {
			t := ""
			switch v := m.Content.(type) {
			case string:
				t = v
			case []any:
				for _, elem := range v {
					if em, ok := elem.(map[string]any); ok {
						if em["type"] == "text" {
							if ts, ok := em["text"].(string); ok {
								t += ts + " "
							}
						}
					}
				}
			}
			t = strings.TrimSpace(t)
			if t != "" {
				if m.Role == "system" {
					parts = append(parts, "[System Instructions]\n"+t)
				} else if m.Role == "assistant" {
					parts = append(parts, "Assistant: "+t)
				} else {
					parts = append(parts, t)
				}
			}
		}
		prompt = strings.Join(parts, "\n\n")
	}
	if prompt == "" {
		prompt = string(payload)
	}
	switch strings.ToLower(model) {
	case "fast", "balanced", "smart", "advanced":
		model = strings.ToLower(model)
	default:
		model = "smart"
	}
	return prompt, model
}

func executeDirectWS(req pluginapi.ExecutorRequest, cred credential) (pluginapi.ExecutorResponse, error) {
	prompt, model := parseUserPrompt(req.Payload, req.Model)
	ans, usageMap, err := runQuickTurn(cred, model, prompt, nil)
	if err != nil {
		return pluginapi.ExecutorResponse{}, pluginabi.NewError("upstream_error", err.Error(), 502)
	}

	created := time.Now().Unix()
	id := "chatcmpl-" + hex.EncodeToString([]byte(genUUID()[:12]))
	
	respJSON := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": ans,
				},
				"finish_reason": "stop",
			},
		},
		"usage": usageMap,
	}
	b, _ := json.Marshal(respJSON)
	return pluginapi.ExecutorResponse{
		Payload: b,
		Headers: http.Header{"Content-Type": {"application/json"}},
		Metadata: map[string]any{"provider": credentialProvider},
	}, nil
}

func executeStreamDirectWS(req pluginapi.ExecutorRequest, streamID string, cred credential) (map[string]any, error) {
	prompt, model := parseUserPrompt(req.Payload, req.Model)
	created := time.Now().Unix()
	id := "chatcmpl-" + hex.EncodeToString([]byte(genUUID()[:12]))

	go func() {
		defer closeStream(streamID, "")
		
		roleSent := false
		onChunk := func(text string) {
			if !roleSent {
				roleSent = true
				firstChunk := map[string]any{
					"id":      id,
					"object":  "chat.completion.chunk",
					"created": created,
					"model":   model,
					"choices": []map[string]any{
						{
							"index": 0,
							"delta": map[string]any{
								"role":    "assistant",
								"content": "",
							},
						},
					},
				}
				b, _ := json.Marshal(firstChunk)
				_ = emitStream(streamID, []byte("data: "+string(b)+"\n\n"))
			}
			chunk := map[string]any{
				"id":      id,
				"object":  "chat.completion.chunk",
				"created": created,
				"model":   model,
				"choices": []map[string]any{
					{
						"index": 0,
						"delta": map[string]any{
							"content": text,
						},
					},
				},
			}
			b, _ := json.Marshal(chunk)
			_ = emitStream(streamID, []byte("data: "+string(b)+"\n\n"))
		}

		_, usageMap, err := runQuickTurn(cred, model, prompt, onChunk)
		if err != nil {
			closeStream(streamID, err.Error())
			return
		}

		// Finish chunk
		finishChunk := map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
			"choices": []map[string]any{
				{
					"index":         0,
					"delta":         map[string]any{},
					"finish_reason": "stop",
				},
			},
			"usage": usageMap,
		}
		fb, _ := json.Marshal(finishChunk)
		_ = emitStream(streamID, []byte("data: "+string(fb)+"\n\n"))
		_ = emitStream(streamID, []byte("data: [DONE]\n\n"))
	}()

	return map[string]any{"headers": http.Header{"Content-Type": {"text/event-stream; charset=utf-8"}}}, nil
}

func runQuickTurn(cred credential, model, text string, deltaCB func(string)) (string, map[string]any, error) {
	// If token might be expired or close to expiry, attempt refresh if refresh_token exists
	if cred.RefreshToken != "" && cred.ClientID != "" {
		if cred.UsableUntil > 0 && time.Now().Unix() > int64(cred.UsableUntil)-120 {
			if refreshed, err := refreshAWSAuth(directSender{}, cred); err == nil {
				cred = refreshed
			}
		}
	}

	workingSessionID := genUUID()
	outpostID := cred.OutpostID
	if outpostID == "" {
		outpostID = genUUID()
	}

	wsHost := cred.WSHost
	if wsHost == "" && cred.TenantURL != "" {
		if u, err := url.Parse(cred.TenantURL); err == nil {
			wsHost = u.Host
		}
	}
	if wsHost == "" {
		return "", nil, errors.New("missing ws_host in credential")
	}

	params := url.Values{
		"surfaceType":      {"QUICK_WORK_DESKTOP_WINDOWS"},
		"token":            {cred.JWT},
		"outpostId":        {outpostID},
		"workingSessionId": {workingSessionID},
		"channel":          {"prod"},
	}
	wsURL := fmt.Sprintf("wss://%s/streaming/quickwork-chat-bidirectional-stream?%s", wsHost, params.Encode())

	headers := http.Header{
		"Origin":          {"file://"},
		"User-Agent":      {quickUA},
		"Accept-Language": {"en"},
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
	}

	conn, _, err := dialer.Dial(wsURL, headers)
	if err != nil {
		return "", nil, fmt.Errorf("websocket dial failed: %w", err)
	}
	defer conn.Close()

	// 1. Send session_connect
	connectPayload := map[string]any{
		"type": "session_connect",
		"payload": map[string]any{
			"surfaceType": "QUICK_WORK_DESKTOP_WINDOWS",
			"outpostId":   outpostID,
			"appVersion":  quickAppVersion,
		},
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, wsEnvelope(connectPayload)); err != nil {
		return "", nil, fmt.Errorf("session_connect write failed: %w", err)
	}

	// 2. Wait for session ack
	ackDeadline := time.Now().Add(10 * time.Second)
	_ = conn.SetReadDeadline(ackDeadline)
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		frames := parseEventStream(msg)
		ack := false
		for _, f := range frames {
			var outer struct {
				Payload string `json:"payload"`
			}
			if json.Unmarshal(f, &outer) == nil && outer.Payload != "" {
				dec, _ := base64.StdEncoding.DecodeString(outer.Payload)
				var inner struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal(dec, &inner)
				if inner.Type == "session_established" || inner.Type == "connected" {
					ack = true
					break
				}
			}
		}
		if ack {
			break
		}
	}

	// 3. Send user_message
	threadID := genUUID()
	msgID := "msg-" + genUUID()[:8]
	traceID := genUUID()

	turnMsg := map[string]any{
		"type": "user_message",
		"payload": map[string]any{
			"traceId":                traceID,
			"text":                   text,
			"send_origin":            "composer",
			"had_physical_input":     true,
			"surfaceType":            "QUICK_WORK_DESKTOP_WINDOWS",
			"outpostId":              outpostID,
			"appVersion":             quickAppVersion,
			"acp_first_text":         1,
			"user_notification":      1,
			"subtask_progress":       1,
			"external_agent_sign_in": 1,
			"recents_promotion":      1,
			"activityType":           "chat",
			"surface":                "desktop",
			"clientVersion":          quickAppVersion,
			"cohort":                 "general_availability",
			"message_id":             msgID,
			"thread_id":              threadID,
			"agent_mode":             model,
			"thinking_effort":        "off",
			"user_timezone":          "UTC",
			"user_locale":            "en",
			"custom_prompt":          nil,
			"chat_agent_id":          nil,
			"auto_routing":           false,
			"web_search_enabled":     false,
			"direct_chat":            false,
			"voice_mode":             false,
		},
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, wsEnvelope(turnMsg)); err != nil {
		return "", nil, fmt.Errorf("user_message send failed: %w", err)
	}

	// 4. Read response deltas
	var answer strings.Builder
	var usageMap map[string]any
	readDeadline := time.Now().Add(120 * time.Second)
	_ = conn.SetReadDeadline(readDeadline)

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		frames := parseEventStream(msg)
		turnDone := false

		for _, f := range frames {
			var outer struct {
				Payload string `json:"payload"`
			}
			if json.Unmarshal(f, &outer) == nil && outer.Payload != "" {
				dec, _ := base64.StdEncoding.DecodeString(outer.Payload)
				var inner struct {
					Type    string          `json:"type"`
					Payload json.RawMessage `json:"payload"`
				}
				if json.Unmarshal(dec, &inner) == nil {
					switch inner.Type {
					case "text_delta":
						var p struct {
							Delta string `json:"delta"`
							Text  string `json:"text"`
						}
						_ = json.Unmarshal(inner.Payload, &p)
						d := p.Delta
						if d == "" {
							d = p.Text
						}
						if d != "" {
							answer.WriteString(d)
							if deltaCB != nil {
								deltaCB(d)
							}
						}
					case "turn_usage":
						var u struct {
							InputTokens  int `json:"input_tokens"`
							OutputTokens int `json:"output_tokens"`
							TotalTokens  int `json:"total_tokens"`
						}
						_ = json.Unmarshal(inner.Payload, &u)
						usageMap = map[string]any{
							"prompt_tokens":     u.InputTokens,
							"completion_tokens": u.OutputTokens,
							"total_tokens":      u.TotalTokens,
						}
					case "message_complete", "text_complete":
						turnDone = true
					case "error", "guardrail_blocked":
						var ep struct {
							Message string `json:"message"`
							Error   string `json:"error"`
						}
						_ = json.Unmarshal(inner.Payload, &ep)
						errMsg := ep.Message
						if errMsg == "" {
							errMsg = ep.Error
						}
						if errMsg == "" {
							errMsg = "quick error: " + inner.Type
						}
						return answer.String(), usageMap, errors.New(errMsg)
					}
				}
			}
		}
		if turnDone {
			break
		}
	}

	if usageMap == nil {
		usageMap = map[string]any{"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}
	}
	return answer.String(), usageMap, nil
}
