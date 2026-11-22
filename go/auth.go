package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const loginEntryID = "qbs-relay"

func authEntries() []pluginapi.AuthProviderEntry {
	return []pluginapi.AuthProviderEntry{{Identifier: loginEntryID, Name: "QBS Relay", Logo: "https://raw.githubusercontent.com/omc-community/omc-qbs-relay/main/assets/logo.png"}}
}

func parseAuth(req pluginapi.AuthParseRequest) pluginapi.AuthParseResponse {
	if strings.ToLower(strings.TrimSpace(req.Provider)) != credentialProvider && strings.TrimSpace(req.Provider) != "" {
		return pluginapi.AuthParseResponse{}
	}
	c, err := readCredential(req.RawJSON)
	if err != nil {
		return pluginapi.AuthParseResponse{}
	}
	return pluginapi.AuthParseResponse{Handled: true, Auth: authDataFor(c)}
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func startLogin(req pluginapi.AuthLoginStartRequest, sender httpSender) (pluginapi.AuthLoginStartResponse, error) {
	token, err := randomToken()
	if err != nil {
		return pluginapi.AuthLoginStartResponse{}, err
	}
	base := strings.TrimRight(req.BaseURL, "/")
	if base == "" {
		base = getConfig().UpstreamBaseURL
	}
	url := fmt.Sprintf("%s/auth/qbs-relay?state=%s", base, token)
	return pluginapi.AuthLoginStartResponse{
		Provider:  "qbs-relay",
		URL:       url,
		State:     token,
		ExpiresAt: time.Now().Add(15 * time.Minute).UTC(),
		Metadata:  map[string]any{"mode": "paste-session"},
	}, nil
}

func pollLogin(req pluginapi.AuthLoginPollRequest, sender httpSender) (pluginapi.AuthLoginPollResponse, error) {
	pending := pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending}
	raw := ""
	if len(req.Metadata) > 0 {
		if v, ok := req.Metadata["credential"]; ok {
			switch x := v.(type) {
			case string:
				raw = x
			case map[string]any:
				b, _ := json.Marshal(x)
				raw = string(b)
			}
		}
	}
	if strings.TrimSpace(raw) == "" {
		return pending, nil
	}
	c, err := readCredential([]byte(raw))
	if err != nil {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: err.Error()}, nil
	}
	if getConfig().RegisterOnAuth {
		if err := registerCredential(sender, c); err != nil {
			return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "register token failed: " + err.Error()}, nil
		}
	}
	return pluginapi.AuthLoginPollResponse{
		Status: pluginapi.AuthLoginStatusSuccess,
		Auth:   authDataFor(c),
	}, nil
}

func refreshAuth() error {
	return errors.New("QBS Relay tokens are refreshed by the Windows provider; re-add the updated session")
}

func registerCredential(sender httpSender, c credential) error {
	h := http.Header{}
	if secret := getConfig().AdminSecret; secret != "" {
		h.Set("X-Admin-Secret", secret)
	}
	payload := map[string]any{
		"account_id":   c.AccountID,
		"jwt":          c.JWT,
		"ws_host":      c.WSHost,
		"outpost_id":   c.OutpostID,
		"usable_until": c.UsableUntil,
	}
	_, _, err := postJSON(sender, "/admin/token", h, payload)
	return err
}
