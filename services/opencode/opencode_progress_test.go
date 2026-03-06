package opencode

import (
	"testing"

	"nairid/models"
)

func TestMapOpenCodeLineToProgress(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *models.AgentProgressPayload
	}{
		{
			name: "tool_use event returns tool_use progress",
			input: `{
				"type": "tool_use",
				"part": {
					"tool": "shell",
					"state": {
						"status": "running",
						"title": "ls -la /tmp"
					}
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     "shell",
				ToolInput:    "ls -la /tmp",
				ToolStatus:   "running",
			},
		},
		{
			name: "tool_use event with completed status",
			input: `{
				"type": "tool_use",
				"part": {
					"tool": "file_editor",
					"state": {
						"status": "completed",
						"title": "/home/user/main.go"
					}
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     "file_editor",
				ToolInput:    "/home/user/main.go",
				ToolStatus:   "completed",
			},
		},
		{
			name: "text event returns text progress",
			input: `{
				"type": "text",
				"part": {
					"text": "I will analyze the codebase now."
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeText,
				TextDelta:    "I will analyze the codebase now.",
			},
		},
		{
			name: "text event with empty text returns nil",
			input: `{
				"type": "text",
				"part": {
					"text": ""
				}
			}`,
			expected: nil,
		},
		{
			name: "step_start event returns step progress",
			input: `{
				"type": "step_start"
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeStep,
				Summary:      "Step started",
			},
		},
		{
			name: "step_finish event with reason returns step progress with reason",
			input: `{
				"type": "step_finish",
				"part": {
					"reason": "end_turn"
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeStep,
				Summary:      "Step completed (reason: end_turn)",
			},
		},
		{
			name: "step_finish event without reason returns step completed",
			input: `{
				"type": "step_finish",
				"part": {}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeStep,
				Summary:      "Step completed",
			},
		},
		{
			name: "non-JSON input returns nil",
			input:    `this is not json`,
			expected: nil,
		},
		{
			name: "unknown type returns nil",
			input: `{
				"type": "unknown_event_type"
			}`,
			expected: nil,
		},
		{
			name: "empty type returns nil",
			input: `{
				"type": ""
			}`,
			expected: nil,
		},
		{
			name: "text event with long text gets truncated",
			input: `{
				"type": "text",
				"part": {
					"text": "` + longString(600) + `"
				}
			}`,
			expected: &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeText,
				TextDelta:    longString(500) + "...",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MapOpenCodeLineToProgress([]byte(tt.input))
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

// longString returns a string of 'a' characters with the given length.
func longString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
