package main

import (
	"encoding/json"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

var pluginVersion = "0.2.1"

type rpcLoginStart struct {
	pluginapi.AuthLoginStartRequest
	CallbackID string `json:"host_callback_id"`
}
type rpcLoginPoll struct {
	pluginapi.AuthLoginPollRequest
	CallbackID string `json:"host_callback_id"`
}
type rpcAuthParse struct {
	pluginapi.AuthParseRequest
	CallbackID string `json:"host_callback_id"`
}
type rpcExecution struct {
	pluginapi.ExecutorRequest
	CallbackID string `json:"host_callback_id"`
}

func decode(raw []byte, out any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if json.Unmarshal(raw, out) != nil {
		return pluginabi.NewError("invalid_request", "invalid plugin request", 400)
	}
	return nil
}

func metadata() any {
	return map[string]any{
		"schema_version": pluginabi.SchemaVersion,
		"metadata": pluginapi.Metadata{
			Name:             "AWS Quick",
			Version:          pluginVersion,
			Author:           "JUNZE",
			GitHubRepository: "https://github.com/omc-community/omc-qbs-relay",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "upstream_base_url", Type: pluginapi.ConfigFieldTypeString, Default: "http://127.0.0.1:19100", Description: "qbs-upstream base URL."},
				{Name: "admin_secret", Type: pluginapi.ConfigFieldTypeString, Description: "X-Admin-Secret used when registering tokens."},
				{Name: "api_key", Type: pluginapi.ConfigFieldTypeString, Description: "Client API key sent to qbs-upstream /v1 endpoints."},
				{Name: "timeout_sec", Type: pluginapi.ConfigFieldTypeInteger, Default: 120, Description: "Upstream request timeout."},
				{Name: "register_on_auth", Type: pluginapi.ConfigFieldTypeBoolean, Default: true, Description: "Register token to upstream after successful auth."},
				{Name: "bridge_mode", Type: pluginapi.ConfigFieldTypeEnum, Default: "upstream", EnumValues: []string{"upstream", "direct"}, Description: "upstream uses qbs-relay-kit pool; direct is reserved for future native WS bridge."},
				{Name: "session_dir", Type: pluginapi.ConfigFieldTypeString, Description: "Optional session directory (matches magpie-aws-quick convention)."},
				{Name: "oauth_port", Type: pluginapi.ConfigFieldTypeInteger, Default: 0, Description: "Optional fixed localhost callback port for browser OAuth."},
				{Name: "oauth_timeout_sec", Type: pluginapi.ConfigFieldTypeInteger, Default: 600, Description: "Browser OAuth timeout."},
			},
		},
		"capabilities": map[string]any{
			"auth_provider":           true,
			"model_provider":          true,
			"executor":                true,
			"management_api":          true,
			"executor_input_formats":  []string{"claude", "openai"},
			"executor_output_formats": []string{"claude", "openai"},
		},
	}
}

func handleMethod(method string, raw []byte) (any, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		var r struct {
			ConfigYAML []byte `json:"config_yaml"`
		}
		if err := decode(raw, &r); err != nil {
			return nil, err
		}
		if err := configure(r.ConfigYAML); err != nil {
			return nil, pluginabi.NewError("invalid_config", err.Error(), 400)
		}
		return metadata(), nil
	case "plugin.quiesce", "plugin.shutdown":
		return map[string]any{}, nil
	case "auth.identifier", "executor.identifier", "model.identifier", "quota.identifier":
		if method == "auth.identifier" {
			return map[string]any{"identifier": loginEntryID, "identifiers": []string{loginEntryID}, "credential_providers": []string{credentialProvider}, "entries": authEntries()}, nil
		}
		return map[string]string{"identifier": credentialProvider}, nil
	case "auth.parse":
		var r rpcAuthParse
		if err := decode(raw, &r); err != nil {
			return nil, err
		}
		return parseAuth(r.AuthParseRequest), nil
	case "auth.login.start":
		var r rpcLoginStart
		if err := decode(raw, &r); err != nil {
			return nil, err
		}
		return startLogin(r.AuthLoginStartRequest, cpaSender{callbackID: r.CallbackID})
	case "auth.login.poll":
		var r rpcLoginPoll
		if err := decode(raw, &r); err != nil {
			return nil, err
		}
		return pollLogin(r.AuthLoginPollRequest, cpaSender{callbackID: r.CallbackID})
	case "management.register":
		return managementRegistration(), nil
	case "management.handle":
		var r struct {
			pluginapi.ManagementRequest
			CallbackID string `json:"host_callback_id"`
		}
		if err := decode(raw, &r); err != nil {
			return nil, err
		}
		if resp, handled := handleManagement(r.ManagementRequest); handled {
			return resp, nil
		}
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "not found"}), nil
	case "auth.refresh":
		return nil, pluginabi.NewError("reauth_required", refreshAuth().Error(), 400)
	case "model.static", "model.for_auth":
		return modelResponse(), nil
	case "executor.execute":
		var r rpcExecution
		if err := decode(raw, &r); err != nil {
			return nil, err
		}
		return execute(r.ExecutorRequest, cpaSender{callbackID: r.CallbackID})
	case "executor.execute_stream":
		var r rpcExecution
		if err := decode(raw, &r); err != nil {
			return nil, err
		}
		var extra struct {
			StreamID string `json:"stream_id"`
		}
		_ = json.Unmarshal(raw, &extra)
		return executeStream(r.ExecutorRequest, r.CallbackID, extra.StreamID, cpaSender{callbackID: r.CallbackID})
	case "executor.count_tokens", "executor.http_request":
		return nil, pluginabi.NewError("not_supported", "unsupported executor method", http.StatusNotImplemented)
	default:
		return nil, pluginabi.NewError("unknown_method", "unknown plugin method", http.StatusNotImplemented)
	}
}
