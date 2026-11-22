package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type directTransportSender struct{ client *http.Client }

func (s directTransportSender) Do(req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	var out pluginapi.HTTPResponse
	r, err := http.NewRequest(req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return out, err
	}
	for k, vs := range req.Headers {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	resp, err := s.client.Do(r)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	out.StatusCode = resp.StatusCode
	out.Headers = resp.Header
	out.Body = readAll(resp.Body)
	return out, nil
}

func readAll(r interface{ Read([]byte) (int, error) }) []byte {
	out := []byte{}
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	return out
}

func TestRegisterAndExecutor(t *testing.T) {
	var mu sync.Mutex
	tokens := map[string]bool{}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/admin/token":
			var d map[string]any
			json.NewDecoder(r.Body).Decode(&d)
			mu.Lock()
			tokens[d["account_id"].(string)] = true
			mu.Unlock()
			w.Write([]byte(`{"ok":true}`))
		case "/v1/messages":
			if r.Header.Get("x-api-key") == "" {
				t.Error("missing x-api-key")
			}
			mu.Lock()
			calls++
			mu.Unlock()
			w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := defaultConfig()
	cfg.UpstreamBaseURL = srv.URL
	cfg.APIKey = "sk-test"
	cfg.AdminSecret = "sec"
	configMu.Lock()
	currentConfig = cfg
	configMu.Unlock()
	sender := directTransportSender{client: srv.Client()}

	for _, id := range []string{"a1", "a2", "a3"} {
		c := credential{Type: credentialProvider, AccountID: id, JWT: "jwt-" + id, WSHost: "qbs-" + id}
		if err := registerCredential(sender, c); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	got := len(tokens)
	mu.Unlock()
	if got != 3 {
		t.Fatalf("registered %d accounts", got)
	}

	c := credential{Type: credentialProvider, AccountID: "a1", JWT: "jwt-a1", WSHost: "qbs-a1"}
	resp, err := execute(pluginapi.ExecutorRequest{
		StorageJSON: c.jsonBytes(),
		Model:       "qbs-claude-opus-5-5",
		Format:      "claude",
		Payload:     []byte(`{"messages":[]}`),
	}, sender)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Payload) == 0 {
		t.Fatal("empty response")
	}
	mu.Lock()
	c2 := calls
	mu.Unlock()
	if c2 != 1 {
		t.Fatalf("calls = %d", c2)
	}
}

func TestParseAuthMultiple(t *testing.T) {
	for _, id := range []string{"a", "b"} {
		c := credential{Type: credentialProvider, AccountID: id, JWT: "jwt", WSHost: "host"}
		res := parseAuth(pluginapi.AuthParseRequest{Provider: credentialProvider, RawJSON: c.jsonBytes()})
		if !res.Handled || res.Auth.Provider != credentialProvider {
			t.Fatalf("bad auth for %s", id)
		}
	}
}

func TestReadCredentialMagpieSessionShape(t *testing.T) {
	raw := []byte(`{
		"provider":"social",
		"tenant_url":"https://qbs-abc.plato.ai.aws.dev",
		"aws_account_id":"722123",
		"email":"user@example.com",
		"jwt":"aoa.token",
		"usable_until":1795280628.89,
		"tokens":{"refresh_token":"r"},
		"client":{"client_id":"c"}
	}`)
	c, err := readCredential(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.AccountID != "722123" || c.JWT != "aoa.token" || c.WSHost != "qbs-abc.plato.ai.aws.dev" {
		t.Fatalf("unexpected credential: %+v", c)
	}
	if c.Email != "user@example.com" {
		t.Fatalf("email = %q", c.Email)
	}
	if c.UsableUntil <= 0 {
		t.Fatal("usable_until not parsed")
	}
}
