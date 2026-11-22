package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type hostStreamResponse struct {
	StreamID   string      `json:"stream_id"`
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers"`
}
type hostReadResponse struct {
	Payload []byte `json:"payload"`
	Error   string `json:"error"`
	Done    bool   `json:"done"`
}

func readHostStream(id string) (hostReadResponse, error) {
	var r hostReadResponse
	if err := hostRPC("host.http.stream_read", map[string]any{"stream_id": id}, &r); err != nil {
		return r, errors.New("upstream stream read failed")
	}
	if r.Error != "" {
		return r, errors.New(r.Error)
	}
	return r, nil
}
func closeHostStream(id string) {
	_ = hostRPC("host.http.stream_close", map[string]any{"stream_id": id}, nil)
}
func emitStream(id string, payload []byte) error {
	return hostRPC("host.stream.emit", map[string]any{"stream_id": id, "payload": payload}, nil)
}
func closeStream(id, message string) {
	_ = hostRPC("host.stream.close", map[string]any{"stream_id": id, "error": message}, nil)
}

func streamRequest(req pluginapi.ExecutorRequest) (pluginapi.HTTPRequest, error) {
	if _, err := readCredential(req.StorageJSON); err != nil {
		return pluginapi.HTTPRequest{}, pluginabi.NewError("invalid_auth", "invalid QBS Relay credential", 401)
	}
	headers := http.Header{}
	for k, vs := range req.Headers {
		headers[k] = append([]string(nil), vs...)
	}
	for _, k := range []string{"Authorization", "x-api-key", "Content-Length"} {
		headers.Del(k)
	}
	for k, v := range apiKeyHeaders() {
		headers[k] = append([]string(nil), v...)
	}
	headers.Set("Content-Type", "application/json")
	path := "/v1/messages"
	if strings.Contains(req.Format, "openai") || strings.Contains(req.SourceFormat, "openai") {
		path = "/v1/chat/completions"
	}
	return pluginapi.HTTPRequest{Method: http.MethodPost, URL: upstreamPath(path), Headers: headers, Body: req.Payload}, nil
}

func executeStream(req pluginapi.ExecutorRequest, callbackID, streamID string, sender httpSender) (map[string]any, error) {
	if streamID == "" {
		return nil, pluginabi.NewError("invalid_request", "stream ID required", 400)
	}
	cred, err := readCredential(req.StorageJSON)
	if err != nil {
		return nil, pluginabi.NewError("invalid_auth", "invalid QBS Relay credential", 401)
	}

	// If bridge_mode is direct, stream directly via WebSocket!
	if getConfig().BridgeMode == "direct" {
		return executeStreamDirectWS(req, streamID, cred)
	}

	httpReq, err := streamRequest(req)
	if err != nil {
		return nil, err
	}
	var upstream hostStreamResponse
	if err := hostRPC("host.http.do_stream", map[string]any{"host_callback_id": callbackID, "request": httpReq}, &upstream); err != nil {
		// Fallback to direct WebSocket if upstream bridge is unavailable
		return executeStreamDirectWS(req, streamID, cred)
	}
	if upstream.StreamID == "" {
		return executeStreamDirectWS(req, streamID, cred)
	}
	if upstream.StatusCode >= 400 || strings.Contains(strings.ToLower(upstream.Headers.Get("Content-Type")), "application/json") {
		var b strings.Builder
		for {
			r, e := readHostStream(upstream.StreamID)
			if e != nil {
				closeHostStream(upstream.StreamID)
				return executeStreamDirectWS(req, streamID, cred)
			}
			b.Write(r.Payload)
			if r.Done {
				break
			}
		}
		closeHostStream(upstream.StreamID)
		return executeStreamDirectWS(req, streamID, cred)
	}
	go func() {
		emitted := false
		defer closeHostStream(upstream.StreamID)
		for {
			r, e := readHostStream(upstream.StreamID)
			if e != nil {
				closeStream(streamID, e.Error())
				return
			}
			if len(r.Payload) > 0 {
				if err := emitStream(streamID, r.Payload); err != nil {
					return
				}
				emitted = true
			}
			if r.Done {
				if !emitted {
					closeStream(streamID, "upstream stream closed before first payload")
				}
				return
			}
		}
	}()
	return map[string]any{"headers": upstream.Headers}, nil
}
