package opencode

import (
	"strings"
	"testing"
)

func TestMapOpenCodeOutputToMessages(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedCount  int
		expectedTypes  []string
		expectError    bool
	}{
		{
			name: "successful parsing of complete response",
			input: `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_123","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_123","part":{"type":"text","text":"Hello!"}}
{"type":"step_finish","timestamp":1759406015885,"sessionID":"ses_123","part":{}}`,
			expectedCount: 3,
			expectedTypes: []string{"step_start", "text", "step_finish"},
			expectError:   false,
		},
		{
			name:          "empty input",
			input:         "",
			expectedCount: 0,
			expectedTypes: []string{},
			expectError:   false,
		},
		{
			name:          "single message",
			input:         `{"type":"text","timestamp":1759406015783,"sessionID":"ses_456","part":{"type":"text","text":"Single message"}}`,
			expectedCount: 1,
			expectedTypes: []string{"text"},
			expectError:   false,
		},
		{
			name: "handles empty lines",
			input: `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_789","part":{}}

{"type":"text","timestamp":1759406015783,"sessionID":"ses_789","part":{"type":"text","text":"Test"}}

`,
			expectedCount: 2,
			expectedTypes: []string{"step_start", "text"},
			expectError:   false,
		},
		{
			name:          "unknown message type",
			input:         `{"type":"unknown_type","timestamp":1759406013703,"sessionID":"ses_abc"}`,
			expectedCount: 1,
			expectedTypes: []string{"unknown_type"},
			expectError:   false,
		},
		{
			name:          "raw error output detected as raw_error type",
			input:         `not json at all`,
			expectedCount: 1,
			expectedTypes: []string{"raw_error"},
			expectError:   false,
		},
		{
			name: "javascript stack trace detected as raw_error",
			input: `154 |           stderr: "pipe",
155 |           stdout: "pipe",
156 |         })
RipgrepExtractionFailedError: RipgrepExtractionFailedError
 data: {
  filepath: "/home/nairid/.local/share/opencode/bin/rg",
  stderr: "tar: unrecognized option: wildcards",
}`,
			expectedCount: 1,
			expectedTypes: []string{"raw_error"},
			expectError:   false,
		},
		{
			name: "non-JSON preamble followed by JSON messages is parsed normally",
			input: `Performing one time database migration, may take a few minutes...
{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_123","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_123","part":{"type":"text","text":"Hello!"}}
{"type":"step_finish","timestamp":1759406015885,"sessionID":"ses_123","part":{}}`,
			expectedCount: 3,
			expectedTypes: []string{"step_start", "text", "step_finish"},
			expectError:   false,
		},
		{
			name: "multiple non-JSON preamble lines followed by JSON messages",
			input: `Performing one time database migration, may take a few minutes...
Migration complete.
{"type":"text","timestamp":1759406015783,"sessionID":"ses_456","part":{"type":"text","text":"Done"}}`,
			expectedCount: 1,
			expectedTypes: []string{"text"},
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages, err := MapOpenCodeOutputToMessages(tt.input)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if len(messages) != tt.expectedCount {
				t.Errorf("Expected %d messages, got %d", tt.expectedCount, len(messages))
			}

			for i, expectedType := range tt.expectedTypes {
				if i < len(messages) && messages[i].GetType() != expectedType {
					t.Errorf("Message %d: expected type %q, got %q", i, expectedType, messages[i].GetType())
				}
			}
		})
	}
}

func TestExtractOpenCodeSessionID(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		expectedID string
	}{
		{
			name: "extracts session ID from first message",
			input: `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_first123","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_first123","part":{"type":"text","text":"Hello"}}`,
			expectedID: "ses_first123",
		},
		{
			name:       "returns unknown for empty messages",
			input:      "",
			expectedID: "unknown",
		},
		{
			name:       "returns unknown when no session ID present",
			input:      `{"type":"text","timestamp":1759406015783}`,
			expectedID: "unknown",
		},
		{
			name:       "extracts from first message with session ID",
			input:      `{"type":"text","timestamp":1759406015783,"sessionID":"ses_fromtext","part":{"type":"text","text":"Test"}}`,
			expectedID: "ses_fromtext",
		},
		{
			name: "extracts session ID when non-JSON preamble precedes JSON output",
			input: `Performing one time database migration, may take a few minutes...
{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_aftermigration","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_aftermigration","part":{"type":"text","text":"Hello"}}`,
			expectedID: "ses_aftermigration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages, err := MapOpenCodeOutputToMessages(tt.input)
			if err != nil {
				t.Fatalf("Failed to parse messages: %v", err)
			}

			sessionID := ExtractOpenCodeSessionID(messages)
			if sessionID != tt.expectedID {
				t.Errorf("Expected session ID %q, got %q", tt.expectedID, sessionID)
			}
		})
	}
}

func TestExtractOpenCodeResult(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedResult string
		expectError    bool
		errorContains  string // substring that should be in the error message
	}{
		{
			name: "extracts text from text message",
			input: `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_123","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_123","part":{"type":"text","text":"Hello! How can I help you?"}}
{"type":"step_finish","timestamp":1759406015885,"sessionID":"ses_123","part":{}}`,
			expectedResult: "Hello! How can I help you?",
			expectError:    false,
		},
		{
			name: "joins multiple text messages with double newline",
			input: `{"type":"text","timestamp":1759406015783,"sessionID":"ses_123","part":{"type":"text","text":"First part."}}
{"type":"text","timestamp":1759406015784,"sessionID":"ses_123","part":{"type":"text","text":"Second part."}}`,
			expectedResult: "First part.\n\nSecond part.",
			expectError:    false,
		},
		{
			name: "joins multiple intermediate text messages with newlines",
			input: `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_123","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_123","part":{"type":"text","text":"Let me query the database."}}
{"type":"tool_use","timestamp":1759406015800,"sessionID":"ses_123","part":{"tool":"read","state":{"status":"completed","title":"schema.sql"}}}
{"type":"text","timestamp":1759406015900,"sessionID":"ses_123","part":{"type":"text","text":"Let me check the tables."}}
{"type":"tool_use","timestamp":1759406015950,"sessionID":"ses_123","part":{"tool":"read","state":{"status":"completed","title":"users.sql"}}}
{"type":"text","timestamp":1759406016000,"sessionID":"ses_123","part":{"type":"text","text":"There are 166 registered users."}}
{"type":"step_finish","timestamp":1759406016100,"sessionID":"ses_123","part":{}}`,
			expectedResult: "Let me query the database.\n\nLet me check the tables.\n\nThere are 166 registered users.",
			expectError:    false,
		},
		{
			name:           "returns error when no text messages",
			input:          `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_123","part":{}}`,
			expectedResult: "",
			expectError:    true,
			errorContains:  "no text message found",
		},
		{
			name:           "returns error for empty input",
			input:          "",
			expectedResult: "",
			expectError:    true,
			errorContains:  "no text message found",
		},
		{
			name:           "ignores text message with empty text",
			input:          `{"type":"text","timestamp":1759406015783,"sessionID":"ses_123","part":{"type":"text","text":""}}`,
			expectedResult: "",
			expectError:    true,
			errorContains:  "no text message found",
		},
		{
			name: "returns opencode error for raw error output",
			input: `154 |           stderr: "pipe",
RipgrepExtractionFailedError: RipgrepExtractionFailedError
 data: {
  stderr: "tar: unrecognized option: wildcards",
}`,
			expectedResult: "",
			expectError:    true,
			errorContains:  "opencode error: RipgrepExtractionFailedError: RipgrepExtractionFailedError",
		},
		{
			name:           "returns opencode error with Error: line extracted",
			input:          `Some prefix\nError: Something went wrong\nStack trace here`,
			expectedResult: "",
			expectError:    true,
			errorContains:  "opencode error:",
		},
		{
			name: "extracts result when non-JSON preamble precedes JSON output",
			input: `Performing one time database migration, may take a few minutes...
{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_123","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_123","part":{"type":"text","text":"Hello! Migration is done."}}
{"type":"step_finish","timestamp":1759406015885,"sessionID":"ses_123","part":{}}`,
			expectedResult: "Hello! Migration is done.",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages, err := MapOpenCodeOutputToMessages(tt.input)
			if err != nil {
				t.Fatalf("Failed to parse messages: %v", err)
			}

			result, err := ExtractOpenCodeResult(messages)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if result != tt.expectedResult {
				t.Errorf("Expected result %q, got %q", tt.expectedResult, result)
			}
			// Check error message contains expected substring
			if tt.expectError && tt.errorContains != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Expected error to contain %q, got %q", tt.errorContains, err.Error())
				}
			}
		})
	}
}

func TestOpenCodeTextMessage_GetType(t *testing.T) {
	msg := OpenCodeTextMessage{
		Type:      "text",
		SessionID: "ses_123",
	}
	if msg.GetType() != "text" {
		t.Errorf("Expected type 'text', got %q", msg.GetType())
	}
}

func TestOpenCodeTextMessage_GetSessionID(t *testing.T) {
	msg := OpenCodeTextMessage{
		Type:      "text",
		SessionID: "ses_123",
	}
	if msg.GetSessionID() != "ses_123" {
		t.Errorf("Expected session ID 'ses_123', got %q", msg.GetSessionID())
	}
}

func TestOpenCodeStepMessage_GetType(t *testing.T) {
	msg := OpenCodeStepMessage{
		Type:      "step_start",
		SessionID: "ses_456",
	}
	if msg.GetType() != "step_start" {
		t.Errorf("Expected type 'step_start', got %q", msg.GetType())
	}
}

func TestOpenCodeStepMessage_GetSessionID(t *testing.T) {
	msg := OpenCodeStepMessage{
		Type:      "step_start",
		SessionID: "ses_456",
	}
	if msg.GetSessionID() != "ses_456" {
		t.Errorf("Expected session ID 'ses_456', got %q", msg.GetSessionID())
	}
}

func TestUnknownOpenCodeMessage_GetType(t *testing.T) {
	msg := UnknownOpenCodeMessage{
		Type:      "custom_type",
		SessionID: "ses_789",
	}
	if msg.GetType() != "custom_type" {
		t.Errorf("Expected type 'custom_type', got %q", msg.GetType())
	}
}

func TestUnknownOpenCodeMessage_GetSessionID(t *testing.T) {
	msg := UnknownOpenCodeMessage{
		Type:      "custom_type",
		SessionID: "ses_789",
	}
	if msg.GetSessionID() != "ses_789" {
		t.Errorf("Expected session ID 'ses_789', got %q", msg.GetSessionID())
	}
}

func TestOpenCodeToolUseMessage_GetType(t *testing.T) {
	msg := OpenCodeToolUseMessage{
		Type:      "tool_use",
		SessionID: "ses_tool123",
	}
	if msg.GetType() != "tool_use" {
		t.Errorf("Expected type 'tool_use', got %q", msg.GetType())
	}
}

func TestOpenCodeToolUseMessage_GetSessionID(t *testing.T) {
	msg := OpenCodeToolUseMessage{
		Type:      "tool_use",
		SessionID: "ses_tool123",
	}
	if msg.GetSessionID() != "ses_tool123" {
		t.Errorf("Expected session ID 'ses_tool123', got %q", msg.GetSessionID())
	}
}

func TestMapOpenCodeOutputToMessages_ToolUse(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedCount int
		expectedTypes []string
	}{
		{
			name: "parses tool_use messages",
			input: `{"type":"step_start","timestamp":1767690266336,"sessionID":"ses_123","part":{}}
{"type":"tool_use","timestamp":1767690268885,"sessionID":"ses_123","part":{"tool":"read","state":{"status":"completed","title":"README.md"}}}
{"type":"step_finish","timestamp":1767690269077,"sessionID":"ses_123","part":{}}`,
			expectedCount: 3,
			expectedTypes: []string{"step_start", "tool_use", "step_finish"},
		},
		{
			name: "parses multiple tool_use messages",
			input: `{"type":"tool_use","timestamp":1767690268885,"sessionID":"ses_123","part":{"tool":"read","state":{"status":"completed","title":"file1.go"}}}
{"type":"tool_use","timestamp":1767690315414,"sessionID":"ses_123","part":{"tool":"edit","state":{"status":"completed","title":"file2.go","metadata":{"filediff":{"additions":5,"deletions":2}}}}}`,
			expectedCount: 2,
			expectedTypes: []string{"tool_use", "tool_use"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages, err := MapOpenCodeOutputToMessages(tt.input)
			if err != nil {
				t.Fatalf("Failed to parse messages: %v", err)
			}

			if len(messages) != tt.expectedCount {
				t.Errorf("Expected %d messages, got %d", tt.expectedCount, len(messages))
			}

			for i, expectedType := range tt.expectedTypes {
				if i < len(messages) && messages[i].GetType() != expectedType {
					t.Errorf("Message %d: expected type %q, got %q", i, expectedType, messages[i].GetType())
				}
			}
		})
	}
}

func TestExtractOpenCodeResult_ToolUseFallback(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedResult string
		expectError    bool
		errorContains  string
	}{
		{
			name: "falls back to edit tool summary when no text",
			input: `{"type":"step_start","timestamp":1767690266336,"sessionID":"ses_123","part":{}}
{"type":"tool_use","timestamp":1767690438167,"sessionID":"ses_123","part":{"tool":"edit","state":{"status":"completed","title":"README.md","metadata":{"filediff":{"file":"README.md","additions":11,"deletions":1}}}}}
{"type":"step_finish","timestamp":1767690438338,"sessionID":"ses_123","part":{}}`,
			expectedResult: "Completed: edited README.md (+11/-1 lines)",
			expectError:    false,
		},
		{
			name: "falls back to multiple edit tool summaries",
			input: `{"type":"tool_use","timestamp":1767690438167,"sessionID":"ses_123","part":{"tool":"edit","state":{"status":"completed","title":"file1.go","metadata":{"filediff":{"additions":5,"deletions":2}}}}}
{"type":"tool_use","timestamp":1767690438200,"sessionID":"ses_123","part":{"tool":"edit","state":{"status":"completed","title":"file2.go","metadata":{"filediff":{"additions":10,"deletions":0}}}}}`,
			expectedResult: "Completed: edited file1.go (+5/-2 lines); edited file2.go (+10/-0 lines)",
			expectError:    false,
		},
		{
			name: "prefers text over tool summaries",
			input: `{"type":"tool_use","timestamp":1767690438167,"sessionID":"ses_123","part":{"tool":"edit","state":{"status":"completed","title":"README.md","metadata":{"filediff":{"additions":11,"deletions":1}}}}}
{"type":"text","timestamp":1767690446667,"sessionID":"ses_123","part":{"text":"Done! Updated the README."}}`,
			expectedResult: "Done! Updated the README.",
			expectError:    false,
		},
		{
			name: "ignores read tool (not an action)",
			input: `{"type":"tool_use","timestamp":1767690268885,"sessionID":"ses_123","part":{"tool":"read","state":{"status":"completed","title":"README.md"}}}
{"type":"step_finish","timestamp":1767690269077,"sessionID":"ses_123","part":{}}`,
			expectedResult: "",
			expectError:    true,
			errorContains:  "no text message found",
		},
		{
			name: "ignores incomplete tool operations",
			input: `{"type":"tool_use","timestamp":1767690438167,"sessionID":"ses_123","part":{"tool":"edit","state":{"status":"pending","title":"README.md"}}}`,
			expectedResult: "",
			expectError:    true,
			errorContains:  "no text message found",
		},
		{
			name: "handles edit without diff stats",
			input: `{"type":"tool_use","timestamp":1767690438167,"sessionID":"ses_123","part":{"tool":"edit","state":{"status":"completed","title":"config.yaml"}}}`,
			expectedResult: "Completed: edited config.yaml",
			expectError:    false,
		},
		{
			name: "handles write tool",
			input: `{"type":"tool_use","timestamp":1767690438167,"sessionID":"ses_123","part":{"tool":"write","state":{"status":"completed","title":"newfile.txt"}}}`,
			expectedResult: "Completed: created newfile.txt",
			expectError:    false,
		},
		{
			name: "handles bash tool",
			input: `{"type":"tool_use","timestamp":1767690438167,"sessionID":"ses_123","part":{"tool":"bash","state":{"status":"completed"}}}`,
			expectedResult: "Completed: ran command",
			expectError:    false,
		},
		{
			name: "handles mixed actionable tools",
			input: `{"type":"tool_use","timestamp":1767690268885,"sessionID":"ses_123","part":{"tool":"read","state":{"status":"completed","title":"README.md"}}}
{"type":"tool_use","timestamp":1767690438167,"sessionID":"ses_123","part":{"tool":"edit","state":{"status":"completed","title":"README.md","metadata":{"filediff":{"additions":5,"deletions":1}}}}}
{"type":"tool_use","timestamp":1767690438200,"sessionID":"ses_123","part":{"tool":"bash","state":{"status":"completed"}}}`,
			expectedResult: "Completed: edited README.md (+5/-1 lines); ran command",
			expectError:    false,
		},
		{
			name: "real-world scenario: only tool calls with edit",
			input: `{"type":"step_start","timestamp":1767690266336,"sessionID":"ses_46d78f7a9ffeT6gMzPsEkxfE2B","part":{"id":"prt_b928ce6d8001tTFwpz62Mnmpai","sessionID":"ses_46d78f7a9ffeT6gMzPsEkxfE2B","messageID":"msg_b928cbe91001IBNq4po6XdSM9r","type":"step-start","snapshot":"4d6ca9cbee79431d34c6d2607f4b985f4c6f3e07"}}
{"type":"tool_use","timestamp":1767690268885,"sessionID":"ses_46d78f7a9ffeT6gMzPsEkxfE2B","part":{"id":"prt_b928cf0b30014EcGz5mOuQlUo0","sessionID":"ses_46d78f7a9ffeT6gMzPsEkxfE2B","messageID":"msg_b928cbe91001IBNq4po6XdSM9r","type":"tool","callID":"call_2c9a27fcc3c34004a3f50d17","tool":"read","state":{"status":"completed","input":{"filePath":"/workspace/repo/README.md"},"output":"file content here","title":"README.md"}}}
{"type":"step_finish","timestamp":1767690269077,"sessionID":"ses_46d78f7a9ffeT6gMzPsEkxfE2B","part":{"id":"prt_b928cf171001TMBxIDw1adRV61","sessionID":"ses_46d78f7a9ffeT6gMzPsEkxfE2B","messageID":"msg_b928cbe91001IBNq4po6XdSM9r","type":"step-finish","reason":"tool-calls","snapshot":"4d6ca9cbee79431d34c6d2607f4b985f4c6f3e07"}}
{"type":"step_start","timestamp":1767690418557,"sessionID":"ses_46d78f7a9ffeT6gMzPsEkxfE2B","part":{"id":"prt_b928f396d0011YFVcRJs9mkPDh","sessionID":"ses_46d78f7a9ffeT6gMzPsEkxfE2B","messageID":"msg_b928da8c2001xqS5wLRBkgyNuc","type":"step-start","snapshot":"4d6ca9cbee79431d34c6d2607f4b985f4c6f3e07"}}
{"type":"tool_use","timestamp":1767690438167,"sessionID":"ses_46d78f7a9ffeT6gMzPsEkxfE2B","part":{"id":"prt_b928f85f8001ySI2243td7LBok","sessionID":"ses_46d78f7a9ffeT6gMzPsEkxfE2B","messageID":"msg_b928da8c2001xqS5wLRBkgyNuc","type":"tool","callID":"call_64d7f2e462a6455d98d6e65d","tool":"edit","state":{"status":"completed","input":{"filePath":"/workspace/repo/README.md","oldString":"old content","newString":"new content"},"output":"","title":"README.md","metadata":{"diagnostics":{},"diff":"diff here","filediff":{"file":"/workspace/repo/README.md","before":"old","after":"new","additions":11,"deletions":1}}}}}
{"type":"step_finish","timestamp":1767690438338,"sessionID":"ses_46d78f7a9ffeT6gMzPsEkxfE2B","part":{"id":"prt_b928f86ac001fZJ2Y9GJ2ltUV8","sessionID":"ses_46d78f7a9ffeT6gMzPsEkxfE2B","messageID":"msg_b928da8c2001xqS5wLRBkgyNuc","type":"step-finish","reason":"tool-calls","snapshot":"555b7451190ed0b930805e92777e36b5dfc1efa9"}}`,
			expectedResult: "Completed: edited README.md (+11/-1 lines)",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages, err := MapOpenCodeOutputToMessages(tt.input)
			if err != nil {
				t.Fatalf("Failed to parse messages: %v", err)
			}

			result, err := ExtractOpenCodeResult(messages)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			if result != tt.expectedResult {
				t.Errorf("Expected result %q, got %q", tt.expectedResult, result)
			}
			if tt.expectError && tt.errorContains != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Expected error to contain %q, got %q", tt.errorContains, err.Error())
				}
			}
		})
	}
}

func TestExtractToolSummary(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		status   string
		title    string
		adds     int
		dels     int
		expected string
	}{
		{
			name:     "edit with diff stats",
			tool:     "edit",
			status:   "completed",
			title:    "main.go",
			adds:     10,
			dels:     3,
			expected: "edited main.go (+10/-3 lines)",
		},
		{
			name:     "edit without diff stats",
			tool:     "edit",
			status:   "completed",
			title:    "config.yaml",
			adds:     0,
			dels:     0,
			expected: "edited config.yaml",
		},
		{
			name:     "write creates file",
			tool:     "write",
			status:   "completed",
			title:    "newfile.txt",
			expected: "created newfile.txt",
		},
		{
			name:     "bash ran command",
			tool:     "bash",
			status:   "completed",
			expected: "ran command",
		},
		{
			name:     "read returns empty (not an action)",
			tool:     "read",
			status:   "completed",
			title:    "file.go",
			expected: "",
		},
		{
			name:     "grep returns empty (not an action)",
			tool:     "grep",
			status:   "completed",
			expected: "",
		},
		{
			name:     "glob returns empty (not an action)",
			tool:     "glob",
			status:   "completed",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := OpenCodeToolUseMessage{
				Type:      "tool_use",
				SessionID: "ses_test",
			}
			msg.Part.Tool = tt.tool
			msg.Part.State.Status = tt.status
			msg.Part.State.Title = tt.title
			msg.Part.State.Metadata.FileDiff.Additions = tt.adds
			msg.Part.State.Metadata.FileDiff.Deletions = tt.dels

			result := extractToolSummary(msg)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}
