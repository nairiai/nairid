package main

import (
	"testing"
)

func TestValidateModelForAgent(t *testing.T) {
	tests := []struct {
		name      string
		agentType string
		model     string
		wantErr   bool
	}{
		// Empty model should be valid for all agents
		{
			name:      "empty model for claude",
			agentType: "claude",
			model:     "",
			wantErr:   false,
		},
		{
			name:      "empty model for cursor",
			agentType: "cursor",
			model:     "",
			wantErr:   false,
		},
		{
			name:      "empty model for codex",
			agentType: "codex",
			model:     "",
			wantErr:   false,
		},
		{
			name:      "empty model for opencode",
			agentType: "opencode",
			model:     "",
			wantErr:   false,
		},
		// Claude accepts any model (validated by Claude CLI itself)
		{
			name:      "claude with model alias",
			agentType: "claude",
			model:     "sonnet",
			wantErr:   false,
		},
		{
			name:      "claude with full model name",
			agentType: "claude",
			model:     "claude-sonnet-4-5-20250929",
			wantErr:   false,
		},
		// Cursor valid models
		{
			name:      "cursor with valid model gpt-5",
			agentType: "cursor",
			model:     "gpt-5",
			wantErr:   false,
		},
		{
			name:      "cursor with valid model sonnet-4",
			agentType: "cursor",
			model:     "sonnet-4",
			wantErr:   false,
		},
		{
			name:      "cursor with valid model sonnet-4-thinking",
			agentType: "cursor",
			model:     "sonnet-4-thinking",
			wantErr:   false,
		},
		{
			name:      "cursor with invalid model",
			agentType: "cursor",
			model:     "invalid-model",
			wantErr:   true,
		},
		// Codex accepts any model
		{
			name:      "codex with any model",
			agentType: "codex",
			model:     "gpt-5",
			wantErr:   false,
		},
		{
			name:      "codex with custom model",
			agentType: "codex",
			model:     "custom-model",
			wantErr:   false,
		},
		// OpenCode expects provider/model format
		{
			name:      "opencode with valid format",
			agentType: "opencode",
			model:     "opencode/grok-code",
			wantErr:   false,
		},
		{
			name:      "opencode with valid format (different provider)",
			agentType: "opencode",
			model:     "anthropic/claude-4",
			wantErr:   false,
		},
		{
			name:      "opencode with invalid format (no slash)",
			agentType: "opencode",
			model:     "gpt-5",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateModelForAgent(tt.agentType, tt.model)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateModelForAgent() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
