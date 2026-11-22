package main

import (
	"encoding/json"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const resourcePrefix = "/v0/resource/plugins/omc-qbs-relay"
const apiPrefix = "/v0/management/plugins/omc-qbs-relay"

func managementRegistration() map[string]any {
	return map[string]any{"routes": []map[string]any{}, "resources": []map[string]any{
		{"path": "/login.html", "menu": "QBS 登录", "description": "Add a QBS session JSON."},
	}}
}

func handleManagement(req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, bool) {
	if req.Path == resourcePrefix+"/login.html" && req.Method == http.MethodGet {
		return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: loginPageHTML}, true
	}
	return pluginapi.ManagementResponse{}, false
}

func jsonResponse(status int, value any) pluginapi.ManagementResponse {
	b, _ := json.Marshal(value)
	return pluginapi.ManagementResponse{StatusCode: status, Headers: http.Header{"Content-Type": {"application/json; charset=utf-8"}}, Body: b}
}
