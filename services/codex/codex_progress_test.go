package codex

import (
	"testing"

	"nairid/models"
)

func TestMapCodexLineToProgress(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *models.AgentProgressPayload
	}{
		{
			name: "item.completed command_execution returns tool_use",
			input: `{
				"type": "item.completed",
				"item": {
					"type": "command_execution",
					"command": "go test ./..."
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     "command_execution",
				ToolInput:    "go test ./...",
				ToolStatus:   "completed",
			},
		},
		{
			name: "item.completed command_execution with status override",
			input: `{
				"type": "item.completed",
				"item": {
					"type": "command_execution",
					"command": "ls",
					"status": "success"
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     "command_execution",
				ToolInput:    "ls",
				ToolStatus:   "success",
			},
		},
		{
			name: "item.completed file_change returns tool_use",
			input: `{
				"type": "item.completed",
				"item": {
					"type": "file_change",
					"file_path": "/home/user/main.go"
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     "file_change",
				ToolInput:    "/home/user/main.go",
				ToolStatus:   "completed",
			},
		},
		{
			name: "item.completed mcp_tool_call returns tool_use with composite name",
			input: `{
				"type": "item.completed",
				"item": {
					"type": "mcp_tool_call",
					"server_name": "postgres",
					"tool_name": "query"
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     "mcp:postgres/query",
				ToolStatus:   "completed",
			},
		},
		{
			name: "item.completed web_search returns tool_use",
			input: `{
				"type": "item.completed",
				"item": {
					"type": "web_search",
					"query": "golang testing"
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     "web_search",
				ToolInput:    "golang testing",
				ToolStatus:   "completed",
			},
		},
		{
			name: "item.completed agent_message returns text",
			input: `{
				"type": "item.completed",
				"item": {
					"type": "agent_message",
					"text": "I found the issue in main.go."
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeText,
				TextDelta:    "I found the issue in main.go.",
			},
		},
		{
			name: "item.completed agent_message with empty text returns nil",
			input: `{
				"type": "item.completed",
				"item": {
					"type": "agent_message",
					"text": ""
				}
			}`,
			expected: nil,
		},
		{
			name: "item.completed reasoning returns step",
			input: `{
				"type": "item.completed",
				"item": {
					"type": "reasoning"
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeStep,
				Summary:      "Reasoning...",
			},
		},
		{
			name: "item.completed todo_list returns step with count",
			input: `{
				"type": "item.completed",
				"item": {
					"type": "todo_list",
					"items": [
						{"text": "task 1", "completed": true},
						{"text": "task 2", "completed": false},
						{"text": "task 3", "completed": true}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeStep,
				Summary:      "Updated checklist: 2/3 items done",
			},
		},
		{
			name: "item.completed todo_list with all done",
			input: `{
				"type": "item.completed",
				"item": {
					"type": "todo_list",
					"items": [
						{"text": "task 1", "completed": true},
						{"text": "task 2", "completed": true}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeStep,
				Summary:      "Updated checklist: 2/2 items done",
			},
		},
		{
			name: "item.completed collab_tool_call returns subagent",
			input: `{
				"type": "item.completed",
				"item": {
					"type": "collab_tool_call",
					"text": "Delegating to sub-agent"
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeSubagent,
				Summary:      "Delegating to sub-agent",
				ToolStatus:   "completed",
			},
		},
		{
			name: "item.completed error type returns tool_use error",
			input: `{
				"type": "item.completed",
				"item": {
					"type": "error",
					"text": "Something went wrong"
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     "error",
				ToolStatus:   "error",
				Summary:      "Something went wrong",
			},
		},
		{
			name: "item.updated command_execution returns running status",
			input: `{
				"type": "item.updated",
				"item": {
					"type": "command_execution",
					"command": "npm install"
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     "command_execution",
				ToolInput:    "npm install",
				ToolStatus:   "running",
			},
		},
		{
			name: "item.updated file_change returns running status",
			input: `{
				"type": "item.updated",
				"item": {
					"type": "file_change",
					"file_path": "/tmp/output.txt"
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     "file_change",
				ToolInput:    "/tmp/output.txt",
				ToolStatus:   "running",
			},
		},
		{
			name: "item.updated agent_message returns text with running semantics",
			input: `{
				"type": "item.updated",
				"item": {
					"type": "agent_message",
					"text": "Working on it..."
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeText,
				TextDelta:    "Working on it...",
			},
		},
		{
			name: "turn.failed returns step error",
			input: `{
				"type": "turn.failed",
				"error": "Rate limit exceeded"
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeStep,
				Summary:      "Turn failed: Rate limit exceeded",
				ToolStatus:   "error",
			},
		},
		{
			name: "error event returns step error",
			input: `{
				"type": "error",
				"message": "Connection timeout"
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeStep,
				Summary:      "Connection timeout",
				ToolStatus:   "error",
			},
		},
		{
			name: "thread.started returns nil (skip)",
			input: `{
				"type": "thread.started"
			}`,
			expected: nil,
		},
		{
			name: "unknown type returns nil",
			input: `{
				"type": "some.unknown.event"
			}`,
			expected: nil,
		},
		{
			name: "invalid JSON returns nil",
			input:    `not valid json`,
			expected: nil,
		},
		{
			name: "item.completed unknown item type returns nil",
			input: `{
				"type": "item.completed",
				"item": {
					"type": "unknown_item_type"
				}
			}`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MapCodexLineToProgress([]byte(tt.input))
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
