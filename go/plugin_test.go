package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type directTransportSender struct{ client *http.Client }

func (s directTransportSender) Do(req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	var out pluginapi.HTTPResponse
	client := s.client
	if client == nil {
		client = http.DefaultClient
	}
	r, err := http.NewRequest(req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return out, err
	}
	for k, vs := range req.Headers {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	resp, err := client.Do(r)
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
	cfg.BridgeMode = "upstream"
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

func TestAuthEntriesAndLoginFlow(t *testing.T) {
	entries := authEntries()
	if len(entries) != 1 || entries[0].Name != "AWS Quick" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	if entries[0].Logo != "https://raw.githubusercontent.com/Merrickk/omc-qbs-relay/main/assets/logo.png" {
		t.Fatalf("unexpected logo path: %s", entries[0].Logo)
	}

	// Test startLogin produces an absolute AWS authorize URL in the default direct mode
	startResp, err := startLogin(pluginapi.AuthLoginStartRequest{Provider: "qbs-relay"}, directTransportSender{})
	if err != nil {
		t.Fatal(err)
	}
	if startResp.State == "" || !strings.HasPrefix(startResp.URL, "https://oidc.") {
		t.Fatalf("unexpected login URL: %s", startResp.URL)
	}
	// CPA adopts this exact token as its session state, so the authorize URL must
	// carry it too: that is what lets one pasted loopback callback finish sign-in.
	if !strings.Contains(startResp.URL, "state="+startResp.State) {
		t.Fatalf("authorize URL does not carry the session state: %s", startResp.URL)
	}

	// Test submit-session via management API
	rawSession := `{"account_id":"722444","jwt":"aoa.test","ws_host":"qbs-cell.example.com","email":"test@example.com"}`
	submitBody, _ := json.Marshal(map[string]string{
		"state":      startResp.State,
		"credential": rawSession,
	})
	mgmtResp, handled := handleManagement(pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Path:   managementAPIPrefix + "/submit-session",
		Body:   submitBody,
	})
	if !handled || mgmtResp.StatusCode != http.StatusOK {
		t.Fatalf("submit-session failed: handled=%v status=%d", handled, mgmtResp.StatusCode)
	}

	// Now pollLogin should instantly resolve with success and auth data!
	cfg := defaultConfig()
	cfg.RegisterOnAuth = false
	configMu.Lock()
	currentConfig = cfg
	configMu.Unlock()

	pollResp, err := pollLogin(pluginapi.AuthLoginPollRequest{
		Provider: "qbs-relay",
		State:    startResp.State,
	}, directTransportSender{})
	if err != nil {
		t.Fatal(err)
	}
	if pollResp.Status != pluginapi.AuthLoginStatusSuccess || pollResp.Auth.Label != "test@example.com" {
		t.Fatalf("pollLogin unexpected: %+v", pollResp)
	}

	// Test logo.png resource route
	logoResp, handledLogo := handleManagement(pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   resourcePrefix + "/logo.png",
	})
	if !handledLogo || logoResp.StatusCode != http.StatusOK || len(logoResp.Body) == 0 {
		t.Fatalf("logo resource route failed: handled=%v status=%d len=%d", handledLogo, logoResp.StatusCode, len(logoResp.Body))
	}

	// Test callback file fallback in pollLogin
	tmpDir := t.TempDir()
	callState := "file-state-12345"
	callCode := `{"account_id":"722999","jwt":"aoa.test","ws_host":"qbs.test.com","email":"file@example.com"}`
	filePath := tmpDir + "/.oauth-qbs-relay-" + callState + ".oauth"
	cbData, _ := json.Marshal(oauthCallbackFilePayload{Code: callCode, State: callState})
	_ = os.WriteFile(filePath, cbData, 0600)

	filePollResp, err := pollLogin(pluginapi.AuthLoginPollRequest{
		Provider: "qbs-relay",
		State:    callState,
		Host:     pluginapi.HostConfigSummary{AuthDir: tmpDir},
	}, directTransportSender{})
	if err != nil {
		t.Fatal(err)
	}
	if filePollResp.Status != pluginapi.AuthLoginStatusSuccess || filePollResp.Auth.Label != "file@example.com" {
		t.Fatalf("unexpected filePollResp: %+v", filePollResp)
	}

	// Test Stage 1 Discovery Callback handling
	unitSender := directTransportSender{}
	unitState := "unit_stage1_state"
	_, unitFlow := buildAWSDiscoveryURL(unitState, 1455)
	userCb := "http://localhost:1455/callback?nonce=" + unitFlow.Nonce + "&account=236147732427&issuerUrl=https%3A%2F%2Fidentitycenter.amazonaws.com%2Fssoins-7223d6ff6fff3554&tenantUrl=https%3A%2F%2Fqbs-cellb8886375-16f2-46de-84f2-ae1c2b595473.dp.appintegrations.us-east-1.prod.plato.ai.aws.dev&idcInstanceId=ssoins-7223d6ff6fff3554&idcRegion=us-east-1"
	cred, nextURL, err := handleAWSCallback(unitSender, unitState, userCb)
	if err != nil {
		t.Fatalf("handleAWSCallback failed: %v", err)
	}
	if nextURL == "" || !strings.HasPrefix(nextURL, "https://oidc.us-east-1.amazonaws.com/authorize") {
		t.Fatalf("unexpected nextURL: %s", nextURL)
	}
	if cred.JWT != "" {
		t.Fatal("expected no cred yet in stage 1")
	}
}
