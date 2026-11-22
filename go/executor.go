package main

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func execute(req pluginapi.ExecutorRequest, sender httpSender) (pluginapi.ExecutorResponse, error) {
	cred, err := readCredential(req.StorageJSON)
	if err != nil {
		return pluginapi.ExecutorResponse{}, pluginabi.NewError("invalid_auth", "invalid QBS Relay credential", 401)
	}
	if len(req.Payload) == 0 {
		return pluginapi.ExecutorResponse{}, pluginabi.NewError("invalid_request", "empty payload", 400)
	}

	// If bridge_mode is direct (or upstream fails), execute via native direct WS
	if getConfig().BridgeMode == "direct" {
		return executeDirectWS(req, cred)
	}

	headers := http.Header{}
	for k, vals := range req.Headers {
		headers[k] = append([]string(nil), vals...)
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
	resp, err := sender.Do(pluginapi.HTTPRequest{Method: http.MethodPost, URL: upstreamPath(path), Headers: headers, Body: req.Payload})
	if err != nil {
		// Fallback to direct native WS if upstream is not reachable
		return executeDirectWS(req, cred)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return pluginapi.ExecutorResponse{}, pluginabi.NewError("upstream_error", "upstream HTTP "+itoa(resp.StatusCode)+": "+string(resp.Body), 502)
	}
	return pluginapi.ExecutorResponse{Payload: resp.Body, Headers: resp.Headers, Metadata: map[string]any{"provider": credentialProvider}}, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

var _ = bytes.MinRead
