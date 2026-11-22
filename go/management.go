package main

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const resourcePrefix = "/v0/resource/plugins/omc-qbs-relay"
const managementAPIPrefix = "/v0/management/plugins/omc-qbs-relay"

//go:embed assets/login.html
var loginPageHTML []byte

//go:embed assets/logo.png
var logoPNG []byte

func managementRegistration() map[string]any {
	return map[string]any{
		"routes": []map[string]any{
			{"method": http.MethodPost, "path": managementAPIPrefix + "/submit-session", "menu": "", "description": "Submit a session JSON to complete OAuth login."},
		},
		"resources": []map[string]any{
			{"path": "/login.html", "menu": "AWS Quick 登录", "description": "Add an AWS Quick session."},
			{"path": "/logo.png", "menu": "", "description": "AWS Quick Logo."},
		},
	}
}

func handleManagement(req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, bool) {
	path := strings.TrimSuffix(req.Path, "/")
	if path == resourcePrefix+"/login.html" && req.Method == http.MethodGet {
		return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: loginPageHTML}, true
	}
	if path == resourcePrefix+"/logo.png" && req.Method == http.MethodGet {
		return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": {"image/png"}}, Body: logoPNG}, true
	}

	if strings.HasPrefix(path, managementAPIPrefix) {
		sub := strings.TrimPrefix(path, managementAPIPrefix)
		if sub == "/submit-session" && req.Method == http.MethodPost {
			var body struct {
				State      string `json:"state"`
				Credential string `json:"credential"`
			}
			if len(req.Body) == 0 || json.Unmarshal(req.Body, &body) != nil || strings.TrimSpace(body.State) == "" || strings.TrimSpace(body.Credential) == "" {
				return jsonResponse(http.StatusBadRequest, map[string]string{"error": "state and credential required"}), true
			}
			c, err := readCredential([]byte(body.Credential))
			if err != nil {
				return jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()}), true
			}
			savePendingSession(strings.TrimSpace(body.State), c)
			return jsonResponse(http.StatusOK, map[string]any{
				"ok":      true,
				"account": c.displayName(),
			}), true
		}
	}
	return pluginapi.ManagementResponse{}, false
}

func jsonResponse(status int, value any) pluginapi.ManagementResponse {
	b, _ := json.Marshal(value)
	return pluginapi.ManagementResponse{StatusCode: status, Headers: http.Header{"Content-Type": {"application/json; charset=utf-8"}}, Body: b}
}
