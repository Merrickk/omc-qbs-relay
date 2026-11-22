package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type httpSender interface {
	Do(pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error)
}

type cpaSender struct{ callbackID string }

func (s cpaSender) Do(req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	var out pluginapi.HTTPResponse
	err := hostRPC("host.http.do", map[string]any{
		"host_callback_id": s.callbackID,
		"request":          req,
	}, &out)
	return out, err
}

type directSender struct{ client *http.Client }

func (d directSender) Do(req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	client := d.client
	if client == nil {
		client = &http.Client{Timeout: timeout()}
	}
	var body io.Reader
	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}
	hreq, err := http.NewRequest(req.Method, req.URL, body)
	if err != nil {
		return pluginapi.HTTPResponse{}, err
	}
	for k, vals := range req.Headers {
		for _, v := range vals {
			hreq.Header.Add(k, v)
		}
	}
	resp, err := client.Do(hreq)
	if err != nil {
		return pluginapi.HTTPResponse{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return pluginapi.HTTPResponse{StatusCode: resp.StatusCode, Headers: resp.Header, Body: raw}, err
}

func upstreamPath(path string) string { return getConfig().UpstreamBaseURL + path }

func postJSON(sender httpSender, path string, headers http.Header, payload any) ([]byte, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	if headers == nil {
		headers = http.Header{}
	}
	headers.Set("Content-Type", "application/json")
	resp, err := sender.Do(pluginapi.HTTPRequest{Method: http.MethodPost, URL: upstreamPath(path), Headers: headers, Body: body})
	if err != nil {
		return resp.Body, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.Body, resp.StatusCode, fmt.Errorf("upstream HTTP %d: %s", resp.StatusCode, string(resp.Body))
	}
	return resp.Body, resp.StatusCode, nil
}

func getJSON(sender httpSender, path string, headers http.Header) ([]byte, int, error) {
	resp, err := sender.Do(pluginapi.HTTPRequest{Method: http.MethodGet, URL: upstreamPath(path), Headers: headers})
	if err != nil {
		return resp.Body, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.Body, resp.StatusCode, fmt.Errorf("upstream HTTP %d: %s", resp.StatusCode, string(resp.Body))
	}
	return resp.Body, resp.StatusCode, nil
}

func apiKeyHeaders() http.Header {
	h := http.Header{}
	if k := getConfig().APIKey; k != "" {
		h.Set("x-api-key", k)
		h.Set("Authorization", "Bearer "+k)
	}
	return h
}

var errNoUpstream = errors.New("upstream unavailable")
