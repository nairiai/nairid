package claude

import (
	"fmt"
	"strings"
	"testing"

	"nairid/models"
)

func TestClaudeProgressTracker_MapLine(t *testing.T) {
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
							"id": "toolu_abc123",
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
				ToolUseID:    "toolu_abc123",
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
							"tool_use_id": "toolu_unknown",
							"is_error": false
						}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolStatus:   "completed",
				ToolUseID:    "toolu_unknown",
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
							"tool_use_id": "toolu_err",
							"is_error": true
						}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolStatus:   "error",
				ToolUseID:    "toolu_err",
			},
		},
		{
			name: "tool_result with string content populates ToolOutput",
			input: `{
				"type": "user",
				"message": {
					"content": [
						{
							"type": "tool_result",
							"tool_use_id": "toolu_out1",
							"is_error": false,
							"content": "file contents here"
						}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolStatus:   "completed",
				ToolUseID:    "toolu_out1",
				ToolOutput:   "file contents here",
			},
		},
		{
			name: "tool_result with content block array extracts text",
			input: `{
				"type": "user",
				"message": {
					"content": [
						{
							"type": "tool_result",
							"tool_use_id": "toolu_out2",
							"is_error": false,
							"content": [{"type": "text", "text": "search results"}]
						}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolStatus:   "completed",
				ToolUseID:    "toolu_out2",
				ToolOutput:   "search results",
			},
		},
		{
			name: "tool_result with empty content leaves ToolOutput empty",
			input: `{
				"type": "user",
				"message": {
					"content": [
						{
							"type": "tool_result",
							"tool_use_id": "toolu_out3",
							"is_error": false
						}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolStatus:   "completed",
				ToolUseID:    "toolu_out3",
			},
		},
		{
			name: "tool_result with is_error=true still populates ToolOutput",
			input: `{
				"type": "user",
				"message": {
					"content": [
						{
							"type": "tool_result",
							"tool_use_id": "toolu_out4",
							"is_error": true,
							"content": "command not found: foo"
						}
					]
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolStatus:   "error",
				ToolUseID:    "toolu_out4",
				ToolOutput:   "command not found: foo",
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
			name:     "invalid JSON returns nil",
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
							"id": "toolu_read1",
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
				ToolUseID:    "toolu_read1",
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
							"id": "toolu_grep1",
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
				ToolUseID:    "toolu_grep1",
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
			tracker := NewClaudeProgressTracker()
			result := tracker.MapLine([]byte(tt.input))
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
			if result.ToolUseID != tt.expected.ToolUseID {
				t.Errorf("ToolUseID: expected %q, got %q", tt.expected.ToolUseID, result.ToolUseID)
			}
			if result.TextDelta != tt.expected.TextDelta {
				t.Errorf("TextDelta: expected %q, got %q", tt.expected.TextDelta, result.TextDelta)
			}
			if result.Summary != tt.expected.Summary {
				t.Errorf("Summary: expected %q, got %q", tt.expected.Summary, result.Summary)
			}
			if result.ToolOutput != tt.expected.ToolOutput {
				t.Errorf("ToolOutput: expected %q, got %q", tt.expected.ToolOutput, result.ToolOutput)
			}
		})
	}
}

func TestClaudeProgressTracker_ToolUsePairing(t *testing.T) {
	t.Run("tool_result gets name from matching tool_use", func(t *testing.T) {
		tracker := NewClaudeProgressTracker()

		// First: tool_use event
		toolUse := tracker.MapLine([]byte(`{
			"type": "assistant",
			"message": {
				"content": [
					{
						"type": "tool_use",
						"id": "toolu_abc",
						"name": "Bash",
						"input": {"command": "ls"}
					}
				]
			}
		}`))
		if toolUse == nil {
			t.Fatal("expected tool_use payload, got nil")
		}
		if toolUse.ToolName != "Bash" {
			t.Errorf("expected ToolName 'Bash', got %q", toolUse.ToolName)
		}
		if toolUse.ToolUseID != "toolu_abc" {
			t.Errorf("expected ToolUseID 'toolu_abc', got %q", toolUse.ToolUseID)
		}

		// Then: matching tool_result
		toolResult := tracker.MapLine([]byte(`{
			"type": "user",
			"message": {
				"content": [
					{
						"type": "tool_result",
						"tool_use_id": "toolu_abc",
						"is_error": false
					}
				]
			}
		}`))
		if toolResult == nil {
			t.Fatal("expected tool_result payload, got nil")
		}
		if toolResult.ToolName != "Bash" {
			t.Errorf("expected ToolName 'Bash' from pairing, got %q", toolResult.ToolName)
		}
		if toolResult.ToolInput != "ls" {
			t.Errorf("expected ToolInput 'ls' from pairing, got %q", toolResult.ToolInput)
		}
		if toolResult.ToolUseID != "toolu_abc" {
			t.Errorf("expected ToolUseID 'toolu_abc', got %q", toolResult.ToolUseID)
		}
		if toolResult.ToolStatus != "completed" {
			t.Errorf("expected ToolStatus 'completed', got %q", toolResult.ToolStatus)
		}
	})

	t.Run("tool_result with unknown tool_use_id has no name", func(t *testing.T) {
		tracker := NewClaudeProgressTracker()

		result := tracker.MapLine([]byte(`{
			"type": "user",
			"message": {
				"content": [
					{
						"type": "tool_result",
						"tool_use_id": "toolu_unknown",
						"is_error": false
					}
				]
			}
		}`))
		if result == nil {
			t.Fatal("expected payload, got nil")
		}
		if result.ToolName != "" {
			t.Errorf("expected empty ToolName for unknown ID, got %q", result.ToolName)
		}
		if result.ToolStatus != "completed" {
			t.Errorf("expected ToolStatus 'completed', got %q", result.ToolStatus)
		}
		if result.ToolUseID != "toolu_unknown" {
			t.Errorf("expected ToolUseID 'toolu_unknown', got %q", result.ToolUseID)
		}
	})

	t.Run("parallel tool_uses with different IDs match correctly", func(t *testing.T) {
		tracker := NewClaudeProgressTracker()

		// Two tool_use events
		tracker.MapLine([]byte(`{
			"type": "assistant",
			"message": {
				"content": [
					{
						"type": "tool_use",
						"id": "toolu_1",
						"name": "Read",
						"input": {"file_path": "/a.go"}
					}
				]
			}
		}`))
		tracker.MapLine([]byte(`{
			"type": "assistant",
			"message": {
				"content": [
					{
						"type": "tool_use",
						"id": "toolu_2",
						"name": "Grep",
						"input": {"pattern": "TODO"}
					}
				]
			}
		}`))

		// Result for second tool first
		result2 := tracker.MapLine([]byte(`{
			"type": "user",
			"message": {
				"content": [
					{
						"type": "tool_result",
						"tool_use_id": "toolu_2",
						"is_error": false
					}
				]
			}
		}`))
		if result2.ToolName != "Grep" {
			t.Errorf("expected ToolName 'Grep', got %q", result2.ToolName)
		}
		if result2.ToolInput != "TODO" {
			t.Errorf("expected ToolInput 'TODO', got %q", result2.ToolInput)
		}

		// Result for first tool
		result1 := tracker.MapLine([]byte(`{
			"type": "user",
			"message": {
				"content": [
					{
						"type": "tool_result",
						"tool_use_id": "toolu_1",
						"is_error": true
					}
				]
			}
		}`))
		if result1.ToolName != "Read" {
			t.Errorf("expected ToolName 'Read', got %q", result1.ToolName)
		}
		if result1.ToolInput != "/a.go" {
			t.Errorf("expected ToolInput '/a.go', got %q", result1.ToolInput)
		}
		if result1.ToolStatus != "error" {
			t.Errorf("expected ToolStatus 'error', got %q", result1.ToolStatus)
		}
	})

	t.Run("tool_result carries ToolOutput from content", func(t *testing.T) {
		tracker := NewClaudeProgressTracker()

		tracker.MapLine([]byte(`{
			"type": "assistant",
			"message": {
				"content": [
					{
						"type": "tool_use",
						"id": "toolu_out",
						"name": "Read",
						"input": {"file_path": "/tmp/test.go"}
					}
				]
			}
		}`))

		result := tracker.MapLine([]byte(`{
			"type": "user",
			"message": {
				"content": [
					{
						"type": "tool_result",
						"tool_use_id": "toolu_out",
						"is_error": false,
						"content": "package main\nfunc main() {}"
					}
				]
			}
		}`))
		if result.ToolName != "Read" {
			t.Errorf("expected ToolName 'Read', got %q", result.ToolName)
		}
		if result.ToolOutput != "package main\nfunc main() {}" {
			t.Errorf("expected ToolOutput content, got %q", result.ToolOutput)
		}
	})

	t.Run("tool_result ToolOutput preserves full content", func(t *testing.T) {
		tracker := NewClaudeProgressTracker()

		longContent := strings.Repeat("x", 600)
		line := fmt.Sprintf(`{
			"type": "user",
			"message": {
				"content": [
					{
						"type": "tool_result",
						"tool_use_id": "toolu_full",
						"is_error": false,
						"content": %q
					}
				]
			}
		}`, longContent)

		result := tracker.MapLine([]byte(line))
		if result == nil {
			t.Fatal("expected payload, got nil")
		}
		if result.ToolOutput != longContent {
			t.Errorf("expected ToolOutput to contain full content (len %d), got len %d", len(longContent), len(result.ToolOutput))
		}
	})
}
