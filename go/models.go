package main

import (
	"encoding/json"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Models mirror magpie-aws-quick: Amazon Quick selects a tier through agent_mode.
// The concrete Bedrock model is resolved server-side and reported in turn_usage.
var modelNames = []string{"fast", "balanced", "smart", "advanced"}

func modelResponse() pluginapi.ModelResponse {
	now := time.Now().Unix()
	models := make([]pluginapi.ModelInfo, 0, len(modelNames))
	for _, name := range modelNames {
		var raw pluginapi.ModelInfo
		b, _ := json.Marshal(map[string]any{"id": name, "object": "model", "created": now, "owned_by": "aws-quick"})
		_ = json.Unmarshal(b, &raw)
		models = append(models, raw)
	}
	return pluginapi.ModelResponse{Provider: credentialProvider, Models: models}
}
