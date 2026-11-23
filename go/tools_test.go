package main

import (
	"testing"
)

func TestToolCallParserStreaming(t *testing.T) {
	parser := NewToolCallParser()

	v1, c1 := parser.Push("Hello, I am thinking. <tool_")
	if v1 != "Hello, I am thinking. " || len(c1) != 0 {
		t.Fatalf("unexpected v1=%q, c1=%v", v1, c1)
	}

	v2, c2 := parser.Push(`call>{"name":"bash","arguments":{"command":"uname -a"}}</tool_call> and done!`)
	if v2 != " and done!" {
		t.Fatalf("unexpected v2=%q", v2)
	}
	if len(c2) != 1 || c2[0].Function.Name != "bash" {
		t.Fatalf("unexpected c2=%v", c2)
	}

	tail, _ := parser.End()
	if tail != "" {
		t.Fatalf("unexpected tail=%q", tail)
	}
}

func TestPrepareTurnContextBudget(t *testing.T) {
	payload := []byte(`{
		"model": "claude-opus-5-5",
		"messages": [
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "What is 1+1?"}
		],
		"tools": [
			{"type": "function", "function": {"name": "calc", "description": "calculator", "parameters": {"type": "object"}}}
		]
	}`)
	turn := prepareTurnContext(payload, "")
	if turn.Model != "smart" {
		t.Fatalf("expected smart, got %s", turn.Model)
	}
	if !turn.HasTools || len(turn.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %v", turn.Tools)
	}
	if turn.SystemPrompt != "You are a helpful assistant." {
		t.Fatalf("unexpected system prompt: %s", turn.SystemPrompt)
	}
}
