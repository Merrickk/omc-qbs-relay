package main

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Supported models for AWS Quick: exactly the 3 models served by Bedrock
type modelDef struct {
	ID   string
	Name string
}

var allModelDefs = []modelDef{
	{ID: "claude-opus-5-5", Name: "Claude Opus 5.5"},
	{ID: "claude-sonnet-5-5", Name: "Claude Sonnet 5.5"},
	{ID: "claude-haiku-5-5", Name: "Claude Haiku 5.5"},
}

func modelResponse() pluginapi.ModelResponse {
	now := time.Now().Unix()
	models := make([]pluginapi.ModelInfo, 0, len(allModelDefs))
	for _, m := range allModelDefs {
		var raw pluginapi.ModelInfo
		b, _ := json.Marshal(map[string]any{
			"id":       m.ID,
			"object":   "model",
			"created":  now,
			"owned_by": "aws-quick",
		})
		_ = json.Unmarshal(b, &raw)
		models = append(models, raw)
	}
	return pluginapi.ModelResponse{Provider: credentialProvider, Models: models}
}

// mapModelToQuickTier maps any model name or alias to the underlying agent_mode for Quick WS
func mapModelToQuickTier(reqModel string) string {
	m := strings.ToLower(strings.TrimSpace(reqModel))
	switch {
	case strings.Contains(m, "haiku") || m == "fast":
		return "fast"
	case strings.Contains(m, "sonnet") || m == "balanced":
		return "balanced"
	case strings.Contains(m, "opus") || m == "smart" || m == "advanced":
		return "smart"
	default:
		return "smart"
	}
}
