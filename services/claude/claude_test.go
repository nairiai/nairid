package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nairid/clients"
	"nairid/core"
	"nairid/services"
)

func TestNewClaudeService(t *testing.T) {
	mockClient := &services.MockClaudeClient{}
	logDir := "/tmp/test-logs"

	service := NewClaudeService(mockClient, logDir, "", nil, nil)

	if service.claudeClient != mockClient {
		t.Error("Expected claude client to be set correctly")
	}

	if service.logDir != logDir {
		t.Errorf("Expected logDir to be %s, got %s", logDir, service.logDir)
	}
}

func TestClaudeService_StartNewConversation(t *testing.T) {
	tests := []struct {
		name            string
		prompt          string
		mockOutput      string
		mockError       error
		expectError     bool
		expectedOutput  string
		expectedSession string
	}{
		{
			name:            "successful conversation start",
			prompt:          "Hello",
			mockOutput:      `{"type":"assistant","message":{"id":"msg_123","type":"message","content":[{"type":"text","text":"Hello! How can I help?"}]},"session_id":"session_123"}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Hello! How can I help?",
			expectedSession: "session_123",
		},
		{
			name:        "client returns error",
			prompt:      "Hello",
			mockOutput:  "",
			mockError:   fmt.Errorf("connection failed"),
			expectError: true,
		},
		{
			name:            "invalid JSON response",
			prompt:          "Hello",
			mockOutput:      "invalid json",
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "(agent returned no response)",
			expectedSession: "unknown",
		},
		{
			name:            "empty prompt",
			prompt:          "",
			mockOutput:      `{"type":"assistant","message":{"id":"msg_123","type":"message","content":[{"type":"text","text":"Please provide a prompt."}]},"session_id":"session_123"}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Please provide a prompt.",
			expectedSession: "session_123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for logs
			tmpDir, err := os.MkdirTemp("", "claude_test_logs_*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Set up mock client
			mockClient := &services.MockClaudeClient{
				StartNewSessionFunc: func(prompt string, options *clients.ClaudeOptions) (string, error) {
					if prompt != tt.prompt {
						t.Errorf("Expected prompt %s, got %s", tt.prompt, prompt)
					}
					return tt.mockOutput, tt.mockError
				},
			}

			service := NewClaudeService(mockClient, tmpDir, "", nil, nil)

			// Execute
			result, err := service.StartNewConversation(tt.prompt)

			// Verify error expectation
			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// If no error expected, verify result
			if !tt.expectError && err == nil {
				if result.Output != tt.expectedOutput {
					t.Errorf("Expected output %q, got %q", tt.expectedOutput, result.Output)
				}
				if result.SessionID != tt.expectedSession {
					t.Errorf("Expected session ID %q, got %q", tt.expectedSession, result.SessionID)
				}
			}

			// Mock verification not needed with function-based mocks
		})
	}
}

func TestClaudeService_StartNewConversationWithSystemPrompt(t *testing.T) {
	tests := []struct {
		name            string
		prompt          string
		systemPrompt    string
		mockOutput      string
		mockError       error
		expectError     bool
		expectedOutput  string
		expectedSession string
	}{
		{
			name:            "successful conversation with system prompt",
			prompt:          "Hello",
			systemPrompt:    "You are a helpful assistant.",
			mockOutput:      `{"type":"assistant","message":{"id":"msg_123","type":"message","content":[{"type":"text","text":"Hello! I'm here to help."}]},"session_id":"session_123"}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Hello! I'm here to help.",
			expectedSession: "session_123",
		},
		{
			name:         "client returns error",
			prompt:       "Hello",
			systemPrompt: "You are a helpful assistant.",
			mockOutput:   "",
			mockError:    fmt.Errorf("connection failed"),
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for logs
			tmpDir, err := os.MkdirTemp("", "claude_test_logs_*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Set up mock client
			mockClient := &services.MockClaudeClient{
				StartNewSessionFunc: func(prompt string, options *clients.ClaudeOptions) (string, error) {
					if prompt != tt.prompt {
						t.Errorf("Expected prompt %s, got %s", tt.prompt, prompt)
					}
					if options == nil || options.SystemPrompt != tt.systemPrompt {
						t.Errorf("Expected system prompt %s, got %s", tt.systemPrompt, options.SystemPrompt)
					}
					return tt.mockOutput, tt.mockError
				},
			}

			service := NewClaudeService(mockClient, tmpDir, "", nil, nil)

			// Execute
			result, err := service.StartNewConversationWithSystemPrompt(tt.prompt, tt.systemPrompt)

			// Verify error expectation
			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// If no error expected, verify result
			if !tt.expectError && err == nil {
				if result.Output != tt.expectedOutput {
					t.Errorf("Expected output %q, got %q", tt.expectedOutput, result.Output)
				}
				if result.SessionID != tt.expectedSession {
					t.Errorf("Expected session ID %q, got %q", tt.expectedSession, result.SessionID)
				}
			}

			// Mock verification not needed with function-based mocks
		})
	}
}

func TestClaudeService_ContinueConversation(t *testing.T) {
	tests := []struct {
		name            string
		sessionID       string
		prompt          string
		mockOutput      string
		mockError       error
		expectError     bool
		expectedOutput  string
		expectedSession string
	}{
		{
			name:            "successful conversation continue",
			sessionID:       "session_123",
			prompt:          "How are you?",
			mockOutput:      `{"type":"assistant","message":{"id":"msg_456","type":"message","content":[{"type":"text","text":"I'm doing well, thank you!"}]},"session_id":"session_123"}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "I'm doing well, thank you!",
			expectedSession: "session_123",
		},
		{
			name:        "client returns error",
			sessionID:   "session_123",
			prompt:      "How are you?",
			mockOutput:  "",
			mockError:   fmt.Errorf("session not found"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for logs
			tmpDir, err := os.MkdirTemp("", "claude_test_logs_*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Set up mock client
			mockClient := &services.MockClaudeClient{
				ContinueSessionFunc: func(sessionID, prompt string, options *clients.ClaudeOptions) (string, error) {
					if sessionID != tt.sessionID {
						t.Errorf("Expected sessionID %s, got %s", tt.sessionID, sessionID)
					}
					if prompt != tt.prompt {
						t.Errorf("Expected prompt %s, got %s", tt.prompt, prompt)
					}
					return tt.mockOutput, tt.mockError
				},
			}

			service := NewClaudeService(mockClient, tmpDir, "", nil, nil)

			// Execute
			result, err := service.ContinueConversation(tt.sessionID, tt.prompt)

			// Verify error expectation
			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// If no error expected, verify result
			if !tt.expectError && err == nil {
				if result.Output != tt.expectedOutput {
					t.Errorf("Expected output %q, got %q", tt.expectedOutput, result.Output)
				}
				if result.SessionID != tt.expectedSession {
					t.Errorf("Expected session ID %q, got %q", tt.expectedSession, result.SessionID)
				}
			}

			// Mock verification not needed with function-based mocks
		})
	}
}

func TestClaudeService_writeClaudeSessionLog(t *testing.T) {
	mockClient := &services.MockClaudeClient{}
	tmpDir, err := os.MkdirTemp("", "claude_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	service := NewClaudeService(mockClient, tmpDir, "", nil, nil)

	rawOutput := "Test Claude session output"
	logPath, err := service.writeClaudeSessionLog(rawOutput)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify log file was created
	if !strings.Contains(logPath, tmpDir) {
		t.Errorf("Log path should be in temp directory")
	}

	if !strings.Contains(logPath, "claude-session-") {
		t.Errorf("Log filename should contain 'claude-session-'")
	}

	// Verify content was written
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	if string(content) != rawOutput {
		t.Errorf("Expected log content %q, got %q", rawOutput, string(content))
	}
}

func TestClaudeService_CleanupOldLogs(t *testing.T) {
	mockClient := &services.MockClaudeClient{}
	tmpDir, err := os.MkdirTemp("", "claude_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	service := NewClaudeService(mockClient, tmpDir, "", nil, nil)

	// Create some test log files with different ages
	now := time.Now()
	oldTime := now.AddDate(0, 0, -10)   // 10 days ago
	recentTime := now.AddDate(0, 0, -3) // 3 days ago

	oldLogFile := filepath.Join(tmpDir, "claude-session-20240101-120000.log")
	recentLogFile := filepath.Join(tmpDir, "claude-session-20240110-120000.log")
	nonClaudeFile := filepath.Join(tmpDir, "other-file.log")

	// Create test files
	if err := os.WriteFile(oldLogFile, []byte("old log"), 0644); err != nil {
		t.Fatalf("Failed to create old log file: %v", err)
	}
	if err := os.WriteFile(recentLogFile, []byte("recent log"), 0644); err != nil {
		t.Fatalf("Failed to create recent log file: %v", err)
	}
	if err := os.WriteFile(nonClaudeFile, []byte("other file"), 0644); err != nil {
		t.Fatalf("Failed to create non-claude file: %v", err)
	}

	// Set file times
	if err := os.Chtimes(oldLogFile, oldTime, oldTime); err != nil {
		t.Fatalf("Failed to set old file time: %v", err)
	}
	if err := os.Chtimes(recentLogFile, recentTime, recentTime); err != nil {
		t.Fatalf("Failed to set recent file time: %v", err)
	}

	// Run cleanup for files older than 7 days
	err = service.CleanupOldLogs(7)
	if err != nil {
		t.Fatalf("Unexpected error during cleanup: %v", err)
	}

	// Verify old file was removed
	if _, err := os.Stat(oldLogFile); !os.IsNotExist(err) {
		t.Errorf("Old log file should have been removed")
	}

	// Verify recent file still exists
	if _, err := os.Stat(recentLogFile); err != nil {
		t.Errorf("Recent log file should still exist")
	}

	// Verify non-claude file still exists
	if _, err := os.Stat(nonClaudeFile); err != nil {
		t.Errorf("Non-claude file should still exist")
	}
}

func TestClaudeService_CleanupOldLogs_InvalidMaxAge(t *testing.T) {
	mockClient := &services.MockClaudeClient{}
	tmpDir, err := os.MkdirTemp("", "claude_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	service := NewClaudeService(mockClient, tmpDir, "", nil, nil)

	// Test invalid maxAgeDays values
	invalidValues := []int{0, -1, -10}
	for _, maxAge := range invalidValues {
		err := service.CleanupOldLogs(maxAge)
		if err == nil {
			t.Errorf("Expected error for maxAgeDays=%d, but got none", maxAge)
		}
	}
}

func TestClaudeService_CleanupOldLogs_NonExistentDirectory(t *testing.T) {
	mockClient := &services.MockClaudeClient{}
	service := NewClaudeService(mockClient, "/non/existent/directory", "", nil, nil)

	// Should not return error for non-existent directory (it's a no-op)
	err := service.CleanupOldLogs(7)
	if err != nil {
		t.Errorf("Expected no error for non-existent directory, but got: %v", err)
	}
}

func TestClaudeService_extractSessionID(t *testing.T) {
	mockClient := &services.MockClaudeClient{}
	service := NewClaudeService(mockClient, "/tmp", "", nil, nil)

	tests := []struct {
		name     string
		messages []services.ClaudeMessage
		expected string
	}{
		{
			name:     "empty messages",
			messages: []services.ClaudeMessage{},
			expected: "unknown",
		},
		{
			name: "single message with session ID",
			messages: []services.ClaudeMessage{
				services.AssistantMessage{
					Type: "assistant",
					Message: struct {
						ID         string            `json:"id"`
						Type       string            `json:"type"`
						Content    []json.RawMessage `json:"content"`
						StopReason string            `json:"stop_reason"`
					}{
						ID:         "msg_123",
						Type:       "message",
						StopReason: "end_turn",
						Content:    []json.RawMessage{},
					},
					SessionID: "session_123",
				},
			},
			expected: "session_123",
		},
		{
			name: "TOS notice before valid message - session ID in second message",
			messages: []services.ClaudeMessage{
				// First message is UnknownClaudeMessage from non-JSON TOS notice line
				services.UnknownClaudeMessage{
					Type:      "unknown",
					SessionID: "", // Empty session ID from unparseable line
				},
				// Second message is valid system init with session ID
				services.SystemMessage{
					Type:      "system",
					Subtype:   "init",
					SessionID: "11c13793-624f-4a38-8f57-ac96d0b7869a",
				},
			},
			expected: "11c13793-624f-4a38-8f57-ac96d0b7869a",
		},
		{
			name: "multiple unknown messages before valid session ID",
			messages: []services.ClaudeMessage{
				services.UnknownClaudeMessage{Type: "unknown", SessionID: ""},
				services.UnknownClaudeMessage{Type: "unknown", SessionID: ""},
				services.AssistantMessage{
					Type: "assistant",
					Message: struct {
						ID         string            `json:"id"`
						Type       string            `json:"type"`
						Content    []json.RawMessage `json:"content"`
						StopReason string            `json:"stop_reason"`
					}{
						ID:      "msg_456",
						Type:    "message",
						Content: []json.RawMessage{},
					},
					SessionID: "valid-session-456",
				},
			},
			expected: "valid-session-456",
		},
		{
			name: "all messages have empty session ID",
			messages: []services.ClaudeMessage{
				services.UnknownClaudeMessage{Type: "unknown", SessionID: ""},
				services.UnknownClaudeMessage{Type: "unknown", SessionID: ""},
			},
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.extractSessionID(tt.messages)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestClaudeService_extractClaudeResult(t *testing.T) {
	mockClient := &services.MockClaudeClient{}
	service := NewClaudeService(mockClient, "/tmp", "", nil, nil)

	tests := []struct {
		name        string
		messages    []services.ClaudeMessage
		expected    string
		expectError bool
	}{
		{
			name:        "empty messages",
			messages:    []services.ClaudeMessage{},
			expected:    "(agent returned no response)",
			expectError: false,
		},
		{
			name: "valid assistant message with text",
			messages: []services.ClaudeMessage{
				services.AssistantMessage{
					Type: "assistant",
					Message: struct {
						ID         string            `json:"id"`
						Type       string            `json:"type"`
						Content    []json.RawMessage `json:"content"`
						StopReason string            `json:"stop_reason"`
					}{
						ID:         "msg_123",
						Type:       "message",
						StopReason: "end_turn",
						Content: []json.RawMessage{
							json.RawMessage(`{"type":"text","text":"Hello World!"}`),
						},
					},
					SessionID: "session_123",
				},
			},
			expected:    "Hello World!",
			expectError: false,
		},
		{
			name: "assistant message without text content",
			messages: []services.ClaudeMessage{
				services.AssistantMessage{
					Type: "assistant",
					Message: struct {
						ID         string            `json:"id"`
						Type       string            `json:"type"`
						Content    []json.RawMessage `json:"content"`
						StopReason string            `json:"stop_reason"`
					}{
						ID:         "msg_123",
						Type:       "message",
						StopReason: "end_turn",
						Content: []json.RawMessage{
							json.RawMessage(`{"type":"image","url":"http://example.com/image.jpg"}`),
						},
					},
					SessionID: "session_123",
				},
			},
			expected:    "(agent returned no response)",
			expectError: false,
		},
		{
			name: "single assistant message - return it",
			messages: []services.ClaudeMessage{
				services.UserMessage{
					Type: "user",
					Message: struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					}{
						Role:    "user",
						Content: json.RawMessage(`"What is the answer?"`),
					},
					SessionID: "session_123",
				},
				services.AssistantMessage{
					Type: "assistant",
					Message: struct {
						ID         string            `json:"id"`
						Type       string            `json:"type"`
						Content    []json.RawMessage `json:"content"`
						StopReason string            `json:"stop_reason"`
					}{
						ID:         "msg_only",
						Type:       "message",
						StopReason: "end_turn",
						Content: []json.RawMessage{
							json.RawMessage(`{"type":"text","text":"Here is the answer: 42"}`),
						},
					},
					SessionID: "session_123",
				},
			},
			expected:    "Here is the answer: 42",
			expectError: false,
		},
		{
			name: "edge case: large first message (10KB) + small second message (67 chars) - return both",
			messages: []services.ClaudeMessage{
				services.UserMessage{
					Type: "user",
					Message: struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					}{
						Role:    "user",
						Content: json.RawMessage(`"Can you provide a detailed breakdown?"`),
					},
					SessionID: "session_edge",
				},
				// Large detailed response (>2000 chars)
				services.AssistantMessage{
					Type: "assistant",
					Message: struct {
						ID         string            `json:"id"`
						Type       string            `json:"type"`
						Content    []json.RawMessage `json:"content"`
						StopReason string            `json:"stop_reason"`
					}{
						ID:         "msg_detailed",
						Type:       "message",
						StopReason: "tool_use",
						Content: []json.RawMessage{
							// Large response (2500+ chars) - generic Lorem Ipsum style content
							json.RawMessage(`{"type":"text","text":"Here is the comprehensive analysis you requested:\n\n## Section A: Overview\nLorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat.\n\n### Subsection A.1: First Component\n- Item alpha: Configuration parameter set to value X\n- Item beta: Configuration parameter set to value Y\n- Item gamma: Configuration parameter set to value Z\n- Item delta: Additional setting enabled\n- Item epsilon: Additional setting disabled\n\n### Subsection A.2: Second Component\n- Property one: Enabled for processing\n- Property two: Disabled for security\n- Property three: Set to default value\n- Property four: Customized setting\n- Property five: Auto-configured\n\n## Section B: Details\nDuis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident.\n\n### Subsection B.1: Configuration Items\n- Parameter A: Active status\n- Parameter B: Inactive status\n- Parameter C: Pending status\n- Parameter D: Completed status\n- Parameter E: Archived status\n- Parameter F: Processing status\n\n### Subsection B.2: Additional Settings\n- Setting one: Value configured\n- Setting two: Value configured\n- Setting three: Value configured\n- Setting four: Value configured\n- Setting five: Value configured\n- Setting six: Value configured\n\n## Section C: Summary\nSed ut perspiciatis unde omnis iste natus error sit voluptatem accusantium doloremque laudantium, totam rem aperiam.\n\n### Key Metrics\n- Total items analyzed: 42\n- Configuration parameters: 18\n- Active settings: 12\n- Optimizations applied: 7\n\nAll configurations follow standard best practices and recommended patterns for optimal performance."}`),
						},
					},
					SessionID: "session_edge",
				},
				services.UserMessage{
					Type: "user",
					Message: struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					}{
						Role:    "user",
						Content: json.RawMessage(`[{"type":"tool_result","content":"42"}]`),
					},
					SessionID: "session_edge",
				},
				// Brief confirmation
				services.AssistantMessage{
					Type: "assistant",
					Message: struct {
						ID         string            `json:"id"`
						Type       string            `json:"type"`
						Content    []json.RawMessage `json:"content"`
						StopReason string            `json:"stop_reason"`
					}{
						ID:         "msg_confirm",
						Type:       "message",
						StopReason: "end_turn",
						Content: []json.RawMessage{
							json.RawMessage(`{"type":"text","text":"Perfect! Analysis complete with 42 items total."}`),
						},
					},
					SessionID: "session_edge",
				},
			},
			// Logic: First message (2500 chars) is 50x+ larger than second (50 chars)
			// Since 50+ > 5, should return BOTH: detailed analysis + confirmation
			expected:    "Here is the comprehensive analysis you requested:\n\n## Section A: Overview\nLorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat.\n\n### Subsection A.1: First Component\n- Item alpha: Configuration parameter set to value X\n- Item beta: Configuration parameter set to value Y\n- Item gamma: Configuration parameter set to value Z\n- Item delta: Additional setting enabled\n- Item epsilon: Additional setting disabled\n\n### Subsection A.2: Second Component\n- Property one: Enabled for processing\n- Property two: Disabled for security\n- Property three: Set to default value\n- Property four: Customized setting\n- Property five: Auto-configured\n\n## Section B: Details\nDuis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident.\n\n### Subsection B.1: Configuration Items\n- Parameter A: Active status\n- Parameter B: Inactive status\n- Parameter C: Pending status\n- Parameter D: Completed status\n- Parameter E: Archived status\n- Parameter F: Processing status\n\n### Subsection B.2: Additional Settings\n- Setting one: Value configured\n- Setting two: Value configured\n- Setting three: Value configured\n- Setting four: Value configured\n- Setting five: Value configured\n- Setting six: Value configured\n\n## Section C: Summary\nSed ut perspiciatis unde omnis iste natus error sit voluptatem accusantium doloremque laudantium, totam rem aperiam.\n\n### Key Metrics\n- Total items analyzed: 42\n- Configuration parameters: 18\n- Active settings: 12\n- Optimizations applied: 7\n\nAll configurations follow standard best practices and recommended patterns for optimal performance.\n\nPerfect! Analysis complete with 42 items total.",
			expectError: false,
		},
		{
			name: "happy path: two similar-sized messages - return only last one",
			messages: []services.ClaudeMessage{
				services.UserMessage{
					Type: "user",
					Message: struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					}{
						Role:    "user",
						Content: json.RawMessage(`"Analyze the architecture"`),
					},
					SessionID: "session_happy",
				},
				// Detailed analysis (>2000 chars) with tool_use
				services.AssistantMessage{
					Type: "assistant",
					Message: struct {
						ID         string            `json:"id"`
						Type       string            `json:"type"`
						Content    []json.RawMessage `json:"content"`
						StopReason string            `json:"stop_reason"`
					}{
						ID:         "msg_analysis",
						Type:       "message",
						StopReason: "tool_use",
						Content: []json.RawMessage{
							// Very long detailed analysis (simulate 31KB)
							json.RawMessage(`{"type":"text","text":"# ULTRA-DETAILED ARCHITECTURE ANALYSIS\n\n## Section 1: Overview\nThis is an extremely detailed analysis spanning many pages...\n[... imagine 31KB of detailed technical analysis here ...]\n\n## Section 50: Conclusion\nAfter analyzing 150+ files and 87 associations, the architecture shows significant complexity with both strengths and weaknesses detailed above."}`),
						},
					},
					SessionID: "session_happy",
				},
				services.UserMessage{
					Type: "user",
					Message: struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					}{
						Role:    "user",
						Content: json.RawMessage(`[{"type":"tool_result","content":"analysis complete"}]`),
					},
					SessionID: "session_happy",
				},
				// Executive summary (>400 chars)
				services.AssistantMessage{
					Type: "assistant",
					Message: struct {
						ID         string            `json:"id"`
						Type       string            `json:"type"`
						Content    []json.RawMessage `json:"content"`
						StopReason string            `json:"stop_reason"`
					}{
						ID:         "msg_summary",
						Type:       "message",
						StopReason: "end_turn",
						Content: []json.RawMessage{
							json.RawMessage(`{"type":"text","text":"## Executive Summary\n\n### The Problem\nArchitecture complexity: 150+ files, 87 associations, significant technical debt.\n\n### The Solution\nPhased refactoring approach with backward compatibility.\n\n### The Gains\n- 64% memory reduction\n- 60% faster response times\n- 3x buffer pool efficiency\n\n### The Plan\nPhase 1-4 over 4 sprints with feature flags.\n\n### ROI\n2 months effort for permanent gains, no breaking changes.\n\n### Next Steps\nSetup Datadog metrics baseline for Go/No-Go decision."}`),
						},
					},
					SessionID: "session_happy",
				},
			},
			// Logic: First message (~300 chars) is NOT 5x larger than second (~500 chars)
			// Since 300/500 < 5, should return ONLY the last message
			expected:    "## Executive Summary\n\n### The Problem\nArchitecture complexity: 150+ files, 87 associations, significant technical debt.\n\n### The Solution\nPhased refactoring approach with backward compatibility.\n\n### The Gains\n- 64% memory reduction\n- 60% faster response times\n- 3x buffer pool efficiency\n\n### The Plan\nPhase 1-4 over 4 sprints with feature flags.\n\n### ROI\n2 months effort for permanent gains, no breaking changes.\n\n### Next Steps\nSetup Datadog metrics baseline for Go/No-Go decision.",
			expectError: false,
		},
		{
			name: "prefers assistant text when result shorter",
			messages: []services.ClaudeMessage{
				// Assistant poem (long)
				services.AssistantMessage{
					Type: "assistant",
					Message: struct {
						ID         string            `json:"id"`
						Type       string            `json:"type"`
						Content    []json.RawMessage `json:"content"`
						StopReason string            `json:"stop_reason"`
					}{
						ID:         "msg_poem",
						Type:       "message",
						StopReason: "tool_use",
						Content: []json.RawMessage{
							json.RawMessage(`{"type":"text","text":"` + strings.Repeat("poem ", 400) + `"}`),
						},
					},
					SessionID: "session_pref",
				},
				// Tool result from user (should be ignored as real user)
				services.UserMessage{
					Type: "user",
					Message: struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					}{
						Role:    "user",
						Content: json.RawMessage(`[{"type":"tool_result","content":""}]`),
					},
					SessionID: "session_pref",
				},
				// Assistant haiku (short)
				services.AssistantMessage{
					Type: "assistant",
					Message: struct {
						ID         string            `json:"id"`
						Type       string            `json:"type"`
						Content    []json.RawMessage `json:"content"`
						StopReason string            `json:"stop_reason"`
					}{
						ID:         "msg_haiku",
						Type:       "message",
						StopReason: "end_turn",
						Content: []json.RawMessage{
							json.RawMessage(`{"type":"text","text":"Code flows like streams"}`),
						},
					},
					SessionID: "session_pref",
				},
				// Result message (shorter than poem)
				services.ResultMessage{
					Type:      "result",
					Subtype:   "success",
					IsError:   false,
					Result:    "Code flows like streams",
					SessionID: "session_pref",
				},
			},
			expected: strings.Repeat("poem ", 400) + "\n\nCode flows like streams",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := service.extractClaudeResult(tt.messages)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if !tt.expectError && result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestClaudeService_handleClaudeClientError(t *testing.T) {
	mockClient := &services.MockClaudeClient{}
	tmpDir, err := os.MkdirTemp("", "claude_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	service := NewClaudeService(mockClient, tmpDir, "", nil, nil)

	tests := []struct {
		name            string
		inputError      error
		operation       string
		expectedContain string
		expectSuccess   bool
		expectedResult  string
	}{
		{
			name:            "nil error",
			inputError:      nil,
			operation:       "test operation",
			expectedContain: "",
		},
		{
			name:            "regular error",
			inputError:      fmt.Errorf("regular error"),
			operation:       "test operation",
			expectedContain: "test operation: regular error",
		},
		{
			name: "claude command error with valid output and no result event recovers as success",
			inputError: &core.ErrClaudeCommandErr{
				Output: `{"type":"assistant","message":{"id":"msg_123","type":"message","content":[{"type":"text","text":"Claude error message"}]},"session_id":"session_123"}`,
				Err:    fmt.Errorf("command failed"),
			},
			operation:      "test operation",
			expectSuccess:  true,
			expectedResult: "Claude error message",
		},
		{
			name: "claude command error with invalid output",
			inputError: &core.ErrClaudeCommandErr{
				Output: "invalid json output",
				Err:    fmt.Errorf("command failed"),
			},
			operation:       "test operation",
			expectedContain: "test operation: claude command failed: command failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.handleClaudeClientError(tt.inputError, tt.operation)

			if tt.inputError == nil {
				if result != nil {
					t.Errorf("Expected nil result for nil input error, got: %v", result)
				}
				return
			}

			if result == nil {
				t.Errorf("Expected error result but got nil")
				return
			}

			if tt.expectSuccess {
				successErr, ok := core.IsClaudeCLISuccessfulResponse(result)
				if !ok {
					t.Fatalf(
						"Expected ErrClaudeCLISuccessfulResponse but got: %T - %v",
						result, result,
					)
				}
				if successErr.Result != tt.expectedResult {
					t.Errorf("Expected result %q, got %q", tt.expectedResult, successErr.Result)
				}
			} else if !strings.Contains(result.Error(), tt.expectedContain) {
				t.Errorf("Expected error to contain %q, got: %v", tt.expectedContain, result.Error())
			}
		})
	}
}

// TestClaudeService_handleClaudeClientError_IsErrorFalse tests that when the CLI exits
// non-zero but the JSON response has is_error=false, we return a special error type
// that can be detected and treated as success by callers.
// This handles the Claude CLI bug: https://github.com/anthropics/claude-code-action/issues/846
func TestClaudeService_handleClaudeClientError_IsErrorFalse(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claude_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	service := NewClaudeService(nil, tmpDir, "", nil, nil)

	tests := []struct {
		name               string
		inputError         error
		operation          string
		expectSuccessErr   bool
		expectedResult     string
		expectedSessionID  string
		expectedErrContain string
	}{
		{
			name: "CLI exit non-zero but is_error=false should return success error",
			inputError: &core.ErrClaudeCommandErr{
				Output: `{"type":"system","subtype":"init","session_id":"sess_123"}
{"type":"assistant","message":{"id":"msg_123","type":"message","content":[{"type":"text","text":"Les données ANPR sont bien renseignées."}]},"session_id":"sess_123"}
{"type":"result","subtype":"success","is_error":false,"result":"Les données ANPR sont bien renseignées.","session_id":"sess_123"}`,
				Err: fmt.Errorf("exit status 1"),
			},
			operation:         "failed to start new Claude session",
			expectSuccessErr:  true,
			expectedResult:    "Les données ANPR sont bien renseignées.",
			expectedSessionID: "sess_123",
		},
		{
			name: "CLI exit non-zero with is_error=true should return regular error",
			inputError: &core.ErrClaudeCommandErr{
				Output: `{"type":"system","subtype":"init","session_id":"sess_456"}
{"type":"assistant","message":{"id":"msg_456","type":"message","content":[{"type":"text","text":"Auth failed"}]},"session_id":"sess_456"}
{"type":"result","subtype":"success","is_error":true,"result":"Your account does not have access.","session_id":"sess_456"}`,
				Err: fmt.Errorf("exit status 1"),
			},
			operation:          "failed to start new Claude session",
			expectSuccessErr:   false,
			expectedErrContain: "Your account does not have access.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.handleClaudeClientError(tt.inputError, tt.operation)

			if result == nil {
				t.Fatal("Expected error result but got nil")
			}

			if tt.expectSuccessErr {
				successErr, ok := core.IsClaudeCLISuccessfulResponse(result)
				if !ok {
					t.Fatalf("Expected ErrClaudeCLISuccessfulResponse but got: %T - %v", result, result)
				}
				if successErr.Result != tt.expectedResult {
					t.Errorf("Expected result %q, got %q", tt.expectedResult, successErr.Result)
				}
				if successErr.SessionID != tt.expectedSessionID {
					t.Errorf("Expected session ID %q, got %q", tt.expectedSessionID, successErr.SessionID)
				}
			} else {
				_, ok := core.IsClaudeCLISuccessfulResponse(result)
				if ok {
					t.Fatal("Did not expect ErrClaudeCLISuccessfulResponse but got one")
				}
				if !strings.Contains(result.Error(), tt.expectedErrContain) {
					t.Errorf("Expected error to contain %q, got: %v", tt.expectedErrContain, result.Error())
				}
			}
		})
	}
}

// TestClaudeService_handleClaudeClientError_NoResultEvent tests recovery when the CLI
// crashes mid-stream before emitting the final result JSON event.
func TestClaudeService_handleClaudeClientError_NoResultEvent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claude_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	service := NewClaudeService(nil, tmpDir, "", nil, nil)

	tests := []struct {
		name              string
		inputError        error
		operation         string
		expectSuccessErr  bool
		expectedResult    string
		expectedSessionID string
	}{
		{
			name: "CLI crash with valid assistant message and no result event",
			inputError: &core.ErrClaudeCommandErr{
				Output: `{"type":"system","subtype":"init","session_id":"sess_abc"}
{"type":"assistant","message":{"id":"msg_001","type":"message","content":[{"type":"text","text":"I found 3 issues in the codebase that need fixing."}],"stop_reason":"end_turn"},"session_id":"sess_abc"}`,
				Err: fmt.Errorf("exit status 1"),
			},
			operation:         "failed to continue Claude session",
			expectSuccessErr:  true,
			expectedResult:    "I found 3 issues in the codebase that need fixing.",
			expectedSessionID: "sess_abc",
		},
		{
			name: "CLI crash with multiple assistant messages and no result event",
			inputError: &core.ErrClaudeCommandErr{
				Output: `{"type":"system","subtype":"init","session_id":"sess_multi"}
{"type":"user","message":{"role":"user","content":"Analyze the code"},"session_id":"sess_multi"}
{"type":"assistant","message":{"id":"msg_010","type":"message","content":[{"type":"text","text":"Let me analyze the code structure."}],"stop_reason":"tool_use"},"session_id":"sess_multi"}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"file contents here"}]},"session_id":"sess_multi"}
{"type":"assistant","message":{"id":"msg_011","type":"message","content":[{"type":"text","text":"The code has a clean architecture."}],"stop_reason":"end_turn"},"session_id":"sess_multi"}`,
				Err: fmt.Errorf("exit status 1"),
			},
			operation:         "failed to continue Claude session",
			expectSuccessErr:  true,
			expectedResult:    "The code has a clean architecture.",
			expectedSessionID: "sess_multi",
		},
		{
			name: "CLI crash with no assistant messages should not recover",
			inputError: &core.ErrClaudeCommandErr{
				Output: `{"type":"system","subtype":"init","session_id":"sess_empty"}`,
				Err:    fmt.Errorf("exit status 1"),
			},
			operation:        "failed to continue Claude session",
			expectSuccessErr: false,
		},
		{
			name: "CLI crash with tool_use only should not recover",
			inputError: &core.ErrClaudeCommandErr{
				Output: `{"type":"system","subtype":"init","session_id":"sess_toolonly"}
{"type":"assistant","message":{"id":"msg_020","type":"message","content":[{"type":"tool_use","id":"tu_1","name":"Read","input":{"file_path":"/tmp/test.go"}}],"stop_reason":"tool_use"},"session_id":"sess_toolonly"}`,
				Err: fmt.Errorf("exit status 1"),
			},
			operation:        "failed to continue Claude session",
			expectSuccessErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.handleClaudeClientError(tt.inputError, tt.operation)
			if result == nil {
				t.Fatal("Expected error result but got nil")
			}
			if tt.expectSuccessErr {
				successErr, ok := core.IsClaudeCLISuccessfulResponse(result)
				if !ok {
					t.Fatalf(
						"Expected ErrClaudeCLISuccessfulResponse but got: %T - %v",
						result, result,
					)
				}
				if successErr.Result != tt.expectedResult {
					t.Errorf("Expected result %q, got %q", tt.expectedResult, successErr.Result)
				}
				if successErr.SessionID != tt.expectedSessionID {
					t.Errorf("Expected session ID %q, got %q", tt.expectedSessionID, successErr.SessionID)
				}
			} else {
				_, ok := core.IsClaudeCLISuccessfulResponse(result)
				if ok {
					t.Fatal("Did not expect ErrClaudeCLISuccessfulResponse")
				}
			}
		})
	}
}

// TestClaudeService_ContinueConversation_RecoverFromCLICrash tests e2e recovery.
func TestClaudeService_ContinueConversation_RecoverFromCLICrash(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claude_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cliOutput := `{"type":"system","subtype":"init","session_id":"sess_crash"}
{"type":"assistant","message":{"id":"msg_100","type":"message","content":[{"type":"text","text":"Examining the codebase now."}],"stop_reason":"tool_use"},"session_id":"sess_crash"}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"src/main.go contents"}]},"session_id":"sess_crash"}
{"type":"assistant","message":{"id":"msg_101","type":"message","content":[{"type":"text","text":"I identified and fixed the bug."}],"stop_reason":"end_turn"},"session_id":"sess_crash"}`

	mockClient := &services.MockClaudeClient{
		ContinueSessionFunc: func(
			sessionID, prompt string, options *clients.ClaudeOptions,
		) (string, error) {
			return cliOutput, &core.ErrClaudeCommandErr{
				Err:    fmt.Errorf("exit status 1"),
				Output: cliOutput,
			}
		},
	}

	service := NewClaudeService(mockClient, tmpDir, "", nil, nil)
	result, err := service.ContinueConversation("sess_crash", "fix the bug")
	if err != nil {
		t.Fatalf("Expected successful recovery but got error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected result but got nil")
	}
	if result.SessionID != "sess_crash" {
		t.Errorf("Expected session ID %q, got %q", "sess_crash", result.SessionID)
	}
	expected := "I identified and fixed the bug."
	if result.Output != expected {
		t.Errorf("Expected output %q, got %q", expected, result.Output)
	}
}

// TestClaudeService_StartNewConversation_RecoverFromCLICrash tests the same for start.
func TestClaudeService_StartNewConversation_RecoverFromCLICrash(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claude_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cliOutput := `{"type":"system","subtype":"init","session_id":"sess_new_crash"}
{"type":"assistant","message":{"id":"msg_200","type":"message","content":[{"type":"text","text":"Project configured successfully."}],"stop_reason":"end_turn"},"session_id":"sess_new_crash"}`

	mockClient := &services.MockClaudeClient{
		StartNewSessionFunc: func(
			prompt string, options *clients.ClaudeOptions,
		) (string, error) {
			return cliOutput, &core.ErrClaudeCommandErr{
				Err:    fmt.Errorf("exit status 1"),
				Output: cliOutput,
			}
		},
	}

	service := NewClaudeService(mockClient, tmpDir, "", nil, nil)
	result, err := service.StartNewConversation("set up the project")
	if err != nil {
		t.Fatalf("Expected successful recovery but got error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected result but got nil")
	}
	if result.SessionID != "sess_new_crash" {
		t.Errorf("Expected session ID %q, got %q", "sess_new_crash", result.SessionID)
	}
	expected := "Project configured successfully."
	if result.Output != expected {
		t.Errorf("Expected output %q, got %q", expected, result.Output)
	}
}

func TestClaudeService_ParseErrorHandling(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claude_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Mock output that doesn't contain valid assistant message (will succeed with fallback message)
	mockClient := &services.MockClaudeClient{
		StartNewSessionFunc: func(prompt string, options *clients.ClaudeOptions) (string, error) {
			if prompt == "test" {
				return "invalid json", nil
			}
			return "", fmt.Errorf("unexpected prompt: %s", prompt)
		},
	}

	service := NewClaudeService(mockClient, tmpDir, "", nil, nil)

	result, err := service.StartNewConversation("test")

	// Should return success with fallback message (not error)
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}

	if result == nil {
		t.Errorf("Expected result but got nil")
		return
	}

	// Check that result contains the fallback message
	if result.Output != "(agent returned no response)" {
		t.Errorf("Expected output '(agent returned no response)', got: %v", result.Output)
	}

	// Mock verification not needed with function-based mocks
}

func TestClaudeService_LargeOutputParses(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claude_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Previously a 5MB single line would cause "bufio.Scanner: token too long".
	// With bufio.Reader, arbitrarily long lines are handled correctly.
	longLine := strings.Repeat("x", 5*1024*1024) // 5MB line
	mockClient := &services.MockClaudeClient{
		StartNewSessionFunc: func(prompt string, options *clients.ClaudeOptions) (string, error) {
			if prompt == "test" {
				return longLine, nil
			}
			return "", fmt.Errorf("unexpected prompt: %s", prompt)
		},
	}

	service := NewClaudeService(mockClient, tmpDir, "", nil, nil)

	result, err := service.StartNewConversation("test")

	// Should succeed (the output is invalid JSON but not a parse error)
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}

	if result == nil {
		t.Errorf("Expected result but got nil")
		return
	}

	// The output is not valid JSON, so no assistant message is extracted
	if result.Output != "(agent returned no response)" {
		t.Errorf("Expected fallback output, got: %v", result.Output)
	}
}

func TestClaudeService_WriteErrorLogHandling(t *testing.T) {
	// Use non-existent parent directory to cause write error
	nonExistentDir := "/non/existent/parent/dir"

	mockClient := &services.MockClaudeClient{
		StartNewSessionFunc: func(prompt string, options *clients.ClaudeOptions) (string, error) {
			if prompt == "test" {
				return `{"type":"assistant","message":{"id":"msg_123","type":"message","content":[{"type":"text","text":"Hello"}]},"session_id":"session_123"}`, nil
			}
			return "", fmt.Errorf("unexpected prompt: %s", prompt)
		},
	}

	service := NewClaudeService(mockClient, nonExistentDir, "", nil, nil)

	// This should still work despite log write error
	result, err := service.StartNewConversation("test")

	if err != nil {
		t.Errorf("Expected successful operation despite log write error, got: %v", err)
	}

	if result == nil || result.Output != "Hello" {
		t.Errorf("Expected valid result despite log write error")
	}

	// Mock verification not needed with function-based mocks
}
