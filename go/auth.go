package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type oauthCallbackFilePayload struct {
	Code  string `json:"code"`
	State string `json:"state"`
	Error string `json:"error"`
}

func readOAuthCallbackFile(authDir, provider, state string) (string, error) {
	if authDir == "" || state == "" {
		return "", nil
	}
	fileName := fmt.Sprintf(".oauth-%s-%s.oauth", strings.TrimSpace(provider), strings.TrimSpace(state))
	filePath := filepath.Join(authDir, fileName)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	_ = os.Remove(filePath)
	var payload oauthCallbackFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	return payload.Code, nil
}

const loginEntryID = "qbs-relay"

var pendingSessions struct {
	sync.RWMutex
	data map[string]credential
}

func savePendingSession(state string, c credential) {
	pendingSessions.Lock()
	defer pendingSessions.Unlock()
	if pendingSessions.data == nil {
		pendingSessions.data = make(map[string]credential)
	}
	pendingSessions.data[state] = c
}

func getPendingSession(state string) (credential, bool) {
	pendingSessions.Lock()
	defer pendingSessions.Unlock()
	if pendingSessions.data == nil {
		return credential{}, false
	}
	c, ok := pendingSessions.data[state]
	if ok {
		delete(pendingSessions.data, state)
	}
	return c, ok
}

func authEntries() []pluginapi.AuthProviderEntry {
	return []pluginapi.AuthProviderEntry{{
		Identifier: loginEntryID,
		Name:       "AWS Quick",
		Logo:       "https://raw.githubusercontent.com/Merrickk/omc-qbs-relay/main/assets/logo.png",
	}}
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
	port := getConfig().OAuthPort
	if port <= 0 {
		port = 1455
	}
	// Try direct Authorize URL first; fallback to discovery URL if register fails
	authURL, _, errDirect := buildDirectAuthorizeURL(sender, token, port)
	if errDirect != nil || authURL == "" {
		authURL, _ = buildAWSDiscoveryURL(token, port)
	}

	return pluginapi.AuthLoginStartResponse{
		Provider:  req.Provider,
		URL:       authURL,
		State:     token,
		ExpiresAt: time.Now().Add(15 * time.Minute).UTC(),
		Metadata: map[string]any{
			"mode": "aws-oidc",
			"port": port,
		},
	}, nil
}

func pollLogin(req pluginapi.AuthLoginPollRequest, sender httpSender) (pluginapi.AuthLoginPollResponse, error) {
	pending := pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending}

	// 1. Check if session was saved via management submit-session API
	if c, ok := getPendingSession(req.State); ok {
		if getConfig().RegisterOnAuth {
			_ = registerCredential(sender, c)
		}
		return pluginapi.AuthLoginPollResponse{
			Status: pluginapi.AuthLoginStatusSuccess,
			Auth:   authDataFor(c),
		}, nil
	}

	// 2. Check if a callback file was written by CPA oauth callback handler
	if req.Host.AuthDir != "" && req.State != "" {
		code, errFile := readOAuthCallbackFile(req.Host.AuthDir, req.Provider, req.State)
		if errFile == nil && code != "" {
			c, nextURL, errAWS := handleAWSCallback(sender, req.State, code)
			if errAWS == nil {
				if nextURL != "" {
					// Stage 1 completed, tell the user/UI the next authorize URL
					return pluginapi.AuthLoginPollResponse{
						Status:  pluginapi.AuthLoginStatusPending,
						Message: "Discovery completed. Please authorize at: " + nextURL,
					}, nil
				}
				if getConfig().RegisterOnAuth {
					_ = registerCredential(sender, c)
				}
				return pluginapi.AuthLoginPollResponse{
					Status: pluginapi.AuthLoginStatusSuccess,
					Auth:   authDataFor(c),
				}, nil
			}
		}
	}

	// 3. Check metadata if passed directly
	raw := ""
	if len(req.Metadata) > 0 {
		if v, ok := req.Metadata["code"]; ok {
			if s, ok := v.(string); ok && s != "" {
				raw = s
			}
		}
		if raw == "" {
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
	}
	if strings.TrimSpace(raw) == "" {
		return pending, nil
	}
	c, nextURL, errAWS := handleAWSCallback(sender, req.State, raw)
	if errAWS != nil {
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: errAWS.Error()}, nil
	}
	if nextURL != "" {
		return pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "Discovery completed. Next URL: " + nextURL,
		}, nil
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

func refreshAuth(req pluginapi.AuthRefreshRequest, sender httpSender) (pluginapi.AuthRefreshResponse, error) {
	c, err := readCredential(req.StorageJSON)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	refreshed, err := refreshAWSAuth(sender, c)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	return pluginapi.AuthRefreshResponse{
		Auth:             authDataFor(refreshed),
		NextRefreshAfter: time.Now().Add(30 * time.Minute),
	}, nil
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
