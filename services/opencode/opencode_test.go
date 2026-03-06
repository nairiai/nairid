package opencode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nairid/clients"
	"nairid/services"
)

func TestNewOpenCodeService(t *testing.T) {
	mockClient := &services.MockOpenCodeClient{}
	logDir := "/tmp/test-logs"
	model := "anthropic/claude-3-5-sonnet"

	service := NewOpenCodeService(mockClient, logDir, model)

	if service.openCodeClient != mockClient {
		t.Error("Expected opencode client to be set correctly")
	}

	if service.logDir != logDir {
		t.Errorf("Expected logDir to be %s, got %s", logDir, service.logDir)
	}

	if service.model != model {
		t.Errorf("Expected model to be %s, got %s", model, service.model)
	}
}

func TestOpenCodeService_StartNewConversation(t *testing.T) {
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
			name:   "successful conversation start",
			prompt: "Hello",
			mockOutput: `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_123abc","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_123abc","part":{"type":"text","text":"Hello! How can I help you today?"}}
{"type":"step_finish","timestamp":1759406015885,"sessionID":"ses_123abc","part":{}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Hello! How can I help you today?",
			expectedSession: "ses_123abc",
		},
		{
			name:        "client returns error",
			prompt:      "Hello",
			mockOutput:  "",
			mockError:   fmt.Errorf("connection failed"),
			expectError: true,
		},
		{
			name:        "no text message in response",
			prompt:      "Hello",
			mockOutput:  `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_456","part":{}}`,
			mockError:   nil,
			expectError: true,
		},
		{
			name:   "empty prompt",
			prompt: "",
			mockOutput: `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_789","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_789","part":{"type":"text","text":"Please provide a prompt."}}
{"type":"step_finish","timestamp":1759406015885,"sessionID":"ses_789","part":{}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Please provide a prompt.",
			expectedSession: "ses_789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for logs
			tmpDir, err := os.MkdirTemp("", "opencode_test_logs_*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Set up mock client
			mockClient := &services.MockOpenCodeClient{
				StartNewSessionFunc: func(prompt string, options *clients.OpenCodeOptions) (string, error) {
					if prompt != tt.prompt {
						t.Errorf("Expected prompt %s, got %s", tt.prompt, prompt)
					}
					return tt.mockOutput, tt.mockError
				},
			}

			service := NewOpenCodeService(mockClient, tmpDir, "")

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
		})
	}
}

func TestOpenCodeService_StartNewConversationWithOptions(t *testing.T) {
	tests := []struct {
		name            string
		prompt          string
		options         *clients.OpenCodeOptions
		serviceModel    string
		mockOutput      string
		mockError       error
		expectError     bool
		expectedOutput  string
		expectedSession string
		verifyOptions   func(*testing.T, *clients.OpenCodeOptions)
	}{
		{
			name:   "options with custom model",
			prompt: "Test prompt",
			options: &clients.OpenCodeOptions{
				Model: "openai/gpt-4",
			},
			serviceModel: "",
			mockOutput: `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_opt1","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_opt1","part":{"type":"text","text":"Response with custom model"}}
{"type":"step_finish","timestamp":1759406015885,"sessionID":"ses_opt1","part":{}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Response with custom model",
			expectedSession: "ses_opt1",
		},
		{
			name:         "service model overrides options model",
			prompt:       "Test prompt",
			options:      &clients.OpenCodeOptions{Model: "option-model"},
			serviceModel: "anthropic/claude-3-5-sonnet",
			mockOutput: `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_opt2","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_opt2","part":{"type":"text","text":"Response with service model"}}
{"type":"step_finish","timestamp":1759406015885,"sessionID":"ses_opt2","part":{}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Response with service model",
			expectedSession: "ses_opt2",
			verifyOptions: func(t *testing.T, opts *clients.OpenCodeOptions) {
				if opts.Model != "anthropic/claude-3-5-sonnet" {
					t.Errorf("Expected model to be overridden to 'anthropic/claude-3-5-sonnet', got '%s'", opts.Model)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for logs
			tmpDir, err := os.MkdirTemp("", "opencode_test_logs_*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Set up mock client
			mockClient := &services.MockOpenCodeClient{
				StartNewSessionFunc: func(prompt string, options *clients.OpenCodeOptions) (string, error) {
					if prompt != tt.prompt {
						t.Errorf("Expected prompt %s, got %s", tt.prompt, prompt)
					}
					if tt.verifyOptions != nil {
						tt.verifyOptions(t, options)
					}
					return tt.mockOutput, tt.mockError
				},
			}

			service := NewOpenCodeService(mockClient, tmpDir, tt.serviceModel)

			// Execute
			result, err := service.StartNewConversationWithOptions(tt.prompt, tt.options)

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
		})
	}
}

func TestOpenCodeService_StartNewConversationWithSystemPrompt(t *testing.T) {
	tests := []struct {
		name            string
		prompt          string
		systemPrompt    string
		mockOutput      string
		mockError       error
		expectError     bool
		expectedOutput  string
		expectedSession string
		verifyPrompt    func(*testing.T, string)
	}{
		{
			name:         "successful conversation with system prompt",
			prompt:       "Hello",
			systemPrompt: "You are a helpful assistant.",
			mockOutput: `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_sys1","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_sys1","part":{"type":"text","text":"Hello! I'm here to help."}}
{"type":"step_finish","timestamp":1759406015885,"sessionID":"ses_sys1","part":{}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Hello! I'm here to help.",
			expectedSession: "ses_sys1",
			verifyPrompt: func(t *testing.T, prompt string) {
				if !strings.Contains(prompt, "# BEHAVIOR INSTRUCTIONS") {
					t.Error("Expected prompt to contain BEHAVIOR INSTRUCTIONS header")
				}
				if !strings.Contains(prompt, "You are a helpful assistant.") {
					t.Error("Expected prompt to contain system prompt")
				}
				if !strings.Contains(prompt, "# USER MESSAGE") {
					t.Error("Expected prompt to contain USER MESSAGE header")
				}
				if !strings.Contains(prompt, "Hello") {
					t.Error("Expected prompt to contain user message")
				}
			},
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
			tmpDir, err := os.MkdirTemp("", "opencode_test_logs_*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Set up mock client
			mockClient := &services.MockOpenCodeClient{
				StartNewSessionFunc: func(prompt string, options *clients.OpenCodeOptions) (string, error) {
					if tt.verifyPrompt != nil {
						tt.verifyPrompt(t, prompt)
					}
					return tt.mockOutput, tt.mockError
				},
			}

			service := NewOpenCodeService(mockClient, tmpDir, "")

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
		})
	}
}

func TestOpenCodeService_StartNewConversationWithDisallowedTools(t *testing.T) {
	// Create temporary directory for logs
	tmpDir, err := os.MkdirTemp("", "opencode_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Set up mock client
	mockClient := &services.MockOpenCodeClient{
		StartNewSessionFunc: func(prompt string, options *clients.OpenCodeOptions) (string, error) {
			return `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_tools","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_tools","part":{"type":"text","text":"Response without disallowed tools"}}
{"type":"step_finish","timestamp":1759406015885,"sessionID":"ses_tools","part":{}}`, nil
		},
	}

	service := NewOpenCodeService(mockClient, tmpDir, "")

	// Execute - OpenCode doesn't support disallowed tools, so this should just work normally
	result, err := service.StartNewConversationWithDisallowedTools("Test prompt", []string{"tool1", "tool2"})

	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}

	if result.Output != "Response without disallowed tools" {
		t.Errorf("Expected output %q, got %q", "Response without disallowed tools", result.Output)
	}
}

func TestOpenCodeService_ContinueConversation(t *testing.T) {
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
			name:      "successful conversation continue",
			sessionID: "ses_123abc",
			prompt:    "How are you?",
			mockOutput: `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_123abc","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_123abc","part":{"type":"text","text":"I'm doing well, thank you!"}}
{"type":"step_finish","timestamp":1759406015885,"sessionID":"ses_123abc","part":{}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "I'm doing well, thank you!",
			expectedSession: "ses_123abc",
		},
		{
			name:        "client returns error",
			sessionID:   "ses_123abc",
			prompt:      "How are you?",
			mockOutput:  "",
			mockError:   fmt.Errorf("session not found"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for logs
			tmpDir, err := os.MkdirTemp("", "opencode_test_logs_*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Set up mock client
			mockClient := &services.MockOpenCodeClient{
				ContinueSessionFunc: func(sessionID, prompt string, options *clients.OpenCodeOptions) (string, error) {
					if sessionID != tt.sessionID {
						t.Errorf("Expected sessionID %s, got %s", tt.sessionID, sessionID)
					}
					if prompt != tt.prompt {
						t.Errorf("Expected prompt %s, got %s", tt.prompt, prompt)
					}
					return tt.mockOutput, tt.mockError
				},
			}

			service := NewOpenCodeService(mockClient, tmpDir, "")

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
		})
	}
}

func TestOpenCodeService_ContinueConversationWithOptions(t *testing.T) {
	tests := []struct {
		name            string
		sessionID       string
		prompt          string
		options         *clients.OpenCodeOptions
		serviceModel    string
		mockOutput      string
		mockError       error
		expectError     bool
		expectedOutput  string
		expectedSession string
	}{
		{
			name:      "continue with custom options",
			sessionID: "ses_opt1",
			prompt:    "Continue prompt",
			options: &clients.OpenCodeOptions{
				Model: "openai/gpt-4",
			},
			serviceModel: "",
			mockOutput: `{"type":"step_start","timestamp":1759406013703,"sessionID":"ses_opt1","part":{}}
{"type":"text","timestamp":1759406015783,"sessionID":"ses_opt1","part":{"type":"text","text":"Continued response"}}
{"type":"step_finish","timestamp":1759406015885,"sessionID":"ses_opt1","part":{}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Continued response",
			expectedSession: "ses_opt1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for logs
			tmpDir, err := os.MkdirTemp("", "opencode_test_logs_*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Set up mock client
			mockClient := &services.MockOpenCodeClient{
				ContinueSessionFunc: func(sessionID, prompt string, options *clients.OpenCodeOptions) (string, error) {
					if sessionID != tt.sessionID {
						t.Errorf("Expected sessionID %s, got %s", tt.sessionID, sessionID)
					}
					if prompt != tt.prompt {
						t.Errorf("Expected prompt %s, got %s", tt.prompt, prompt)
					}
					return tt.mockOutput, tt.mockError
				},
			}

			service := NewOpenCodeService(mockClient, tmpDir, tt.serviceModel)

			// Execute
			result, err := service.ContinueConversationWithOptions(tt.sessionID, tt.prompt, tt.options)

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
		})
	}
}

func TestOpenCodeService_CleanupOldLogs(t *testing.T) {
	mockClient := &services.MockOpenCodeClient{}
	tmpDir, err := os.MkdirTemp("", "opencode_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	service := NewOpenCodeService(mockClient, tmpDir, "")

	// Create some test log files with different ages
	now := time.Now()
	oldTime := now.AddDate(0, 0, -10)   // 10 days ago
	recentTime := now.AddDate(0, 0, -3) // 3 days ago

	oldLogFile := filepath.Join(tmpDir, "opencode-session-20240101-120000.log")
	recentLogFile := filepath.Join(tmpDir, "opencode-session-20240110-120000.log")
	nonOpenCodeFile := filepath.Join(tmpDir, "other-file.log")

	// Create test files
	if err := os.WriteFile(oldLogFile, []byte("old log"), 0644); err != nil {
		t.Fatalf("Failed to create old log file: %v", err)
	}
	if err := os.WriteFile(recentLogFile, []byte("recent log"), 0644); err != nil {
		t.Fatalf("Failed to create recent log file: %v", err)
	}
	if err := os.WriteFile(nonOpenCodeFile, []byte("other file"), 0644); err != nil {
		t.Fatalf("Failed to create non-opencode file: %v", err)
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

	// Verify non-opencode file still exists
	if _, err := os.Stat(nonOpenCodeFile); err != nil {
		t.Errorf("Non-opencode file should still exist")
	}
}

func TestOpenCodeService_CleanupOldLogs_InvalidMaxAge(t *testing.T) {
	mockClient := &services.MockOpenCodeClient{}
	tmpDir, err := os.MkdirTemp("", "opencode_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	service := NewOpenCodeService(mockClient, tmpDir, "")

	// Test invalid maxAgeDays values
	invalidValues := []int{0, -1, -10}
	for _, maxAge := range invalidValues {
		err := service.CleanupOldLogs(maxAge)
		if err == nil {
			t.Errorf("Expected error for maxAgeDays=%d, but got none", maxAge)
		}
	}
}

func TestOpenCodeService_CleanupOldLogs_NonExistentDirectory(t *testing.T) {
	mockClient := &services.MockOpenCodeClient{}
	service := NewOpenCodeService(mockClient, "/non/existent/directory", "")

	// Should not return error for non-existent directory (it's a no-op)
	err := service.CleanupOldLogs(7)
	if err != nil {
		t.Errorf("Expected no error for non-existent directory, but got: %v", err)
	}
}

func TestOpenCodeService_AgentName(t *testing.T) {
	mockClient := &services.MockOpenCodeClient{}
	service := NewOpenCodeService(mockClient, "/tmp", "")

	agentName := service.AgentName()
	expectedName := "opencode"

	if agentName != expectedName {
		t.Errorf("Expected agent name %q, got %q", expectedName, agentName)
	}
}

