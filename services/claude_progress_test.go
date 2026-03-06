package services

import (
	"testing"

	"nairid/models"
)

func TestMapClaudeLineToProgress(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *models.AgentProgressPayload
	}{
		{
			name: "assistant message with tool_use content",
			input: `{
				"type": "assistant",
				"message": {
					"content": [
						{
							"type": "tool_use",
							"name": "Bash",
							"input": {"command": "ls -la"}
						}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     "Bash",
				ToolInput:    "ls -la",
				ToolStatus:   "running",
			},
		},
		{
			name: "assistant message with text content",
			input: `{
				"type": "assistant",
				"message": {
					"content": [
						{
							"type": "text",
							"text": "Hello, I will help you with that."
						}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeText,
				TextDelta:    "Hello, I will help you with that.",
			},
		},
		{
			name: "assistant message with empty text content returns nil",
			input: `{
				"type": "assistant",
				"message": {
					"content": [
						{
							"type": "text",
							"text": ""
						}
					]
				}
			}`,
			expected: nil,
		},
		{
			name: "user message with tool_result success",
			input: `{
				"type": "user",
				"message": {
					"content": [
						{
							"type": "tool_result",
							"is_error": false
						}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolStatus:   "completed",
			},
		},
		{
			name: "user message with tool_result error",
			input: `{
				"type": "user",
				"message": {
					"content": [
						{
							"type": "tool_result",
							"is_error": true
						}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolStatus:   "error",
			},
		},
		{
			name: "tool_progress message returns tool heartbeat",
			input: `{
				"type": "tool_progress",
				"tool_name": "Bash",
				"elapsed_time_seconds": 15
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolHeartbeat,
				ToolName:     "Bash",
				Summary:      "Running for 15s...",
				ToolStatus:   "running",
			},
		},
		{
			name: "system task_started returns subagent running",
			input: `{
				"type": "system",
				"subtype": "task_started",
				"description": "Analyzing the codebase"
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeSubagent,
				Summary:      "Subagent started: Analyzing the codebase",
				ToolStatus:   "running",
			},
		},
		{
			name: "system task_progress returns subagent running",
			input: `{
				"type": "system",
				"subtype": "task_progress",
				"description": "Still working on analysis"
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeSubagent,
				Summary:      "Still working on analysis",
				ToolStatus:   "running",
			},
		},
		{
			name: "system task_notification completed returns subagent completed",
			input: `{
				"type": "system",
				"subtype": "task_notification",
				"status": "completed",
				"summary": "Analysis done"
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeSubagent,
				Summary:      "Analysis done",
				ToolStatus:   "completed",
			},
		},
		{
			name: "system task_notification failed returns subagent error",
			input: `{
				"type": "system",
				"subtype": "task_notification",
				"status": "failed",
				"summary": "Task crashed"
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeSubagent,
				Summary:      "Task crashed",
				ToolStatus:   "error",
			},
		},
		{
			name: "system task_notification stopped returns subagent error",
			input: `{
				"type": "system",
				"subtype": "task_notification",
				"status": "stopped",
				"summary": "Task was stopped"
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeSubagent,
				Summary:      "Task was stopped",
				ToolStatus:   "error",
			},
		},
		{
			name: "system init (non-progress) returns nil",
			input: `{
				"type": "system",
				"subtype": "init"
			}`,
			expected: nil,
		},
		{
			name: "result message returns nil",
			input: `{
				"type": "result"
			}`,
			expected: nil,
		},
		{
			name: "invalid JSON returns nil",
			input:    `not valid json at all`,
			expected: nil,
		},
		{
			name: "tool_use with Read tool extracts file_path",
			input: `{
				"type": "assistant",
				"message": {
					"content": [
						{
							"type": "tool_use",
							"name": "Read",
							"input": {"file_path": "/tmp/test.go"}
						}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     "Read",
				ToolInput:    "/tmp/test.go",
				ToolStatus:   "running",
			},
		},
		{
			name: "tool_use with Grep tool extracts pattern",
			input: `{
				"type": "assistant",
				"message": {
					"content": [
						{
							"type": "tool_use",
							"name": "Grep",
							"input": {"pattern": "TODO"}
						}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     "Grep",
				ToolInput:    "TODO",
				ToolStatus:   "running",
			},
		},
		{
			name: "user message with non-array content returns nil",
			input: `{
				"type": "user",
				"message": {
					"content": "plain text"
				}
			}`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MapClaudeLineToProgress([]byte(tt.input))
			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatalf("expected %+v, got nil", tt.expected)
			}
			if result.ProgressType != tt.expected.ProgressType {
				t.Errorf("ProgressType: expected %q, got %q", tt.expected.ProgressType, result.ProgressType)
			}
			if result.ToolName != tt.expected.ToolName {
				t.Errorf("ToolName: expected %q, got %q", tt.expected.ToolName, result.ToolName)
			}
			if result.ToolInput != tt.expected.ToolInput {
				t.Errorf("ToolInput: expected %q, got %q", tt.expected.ToolInput, result.ToolInput)
			}
			if result.ToolStatus != tt.expected.ToolStatus {
				t.Errorf("ToolStatus: expected %q, got %q", tt.expected.ToolStatus, result.ToolStatus)
			}
			if result.TextDelta != tt.expected.TextDelta {
				t.Errorf("TextDelta: expected %q, got %q", tt.expected.TextDelta, result.TextDelta)
			}
			if result.Summary != tt.expected.Summary {
				t.Errorf("Summary: expected %q, got %q", tt.expected.Summary, result.Summary)
			}
		})
	}
}
