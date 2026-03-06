package codex

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

func TestNewCodexService(t *testing.T) {
	mockClient := &services.MockCodexClient{}
	logDir := "/tmp/test-logs"
	model := "gpt-5"

	service := NewCodexService(mockClient, logDir, model)

	if service.codexClient != mockClient {
		t.Error("Expected codex client to be set correctly")
	}

	if service.logDir != logDir {
		t.Errorf("Expected logDir to be %s, got %s", logDir, service.logDir)
	}

	if service.model != model {
		t.Errorf("Expected model to be %s, got %s", model, service.model)
	}
}

func TestCodexService_StartNewConversation(t *testing.T) {
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
			mockOutput: `{"type":"thread.started","thread_id":"thread_123"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"Hello! How can I help?"}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Hello! How can I help?",
			expectedSession: "thread_123",
		},
		{
			name:        "client returns error",
			prompt:      "Hello",
			mockOutput:  "",
			mockError:   fmt.Errorf("connection failed"),
			expectError: true,
		},
		{
			name:        "invalid JSON response",
			prompt:      "Hello",
			mockOutput:  "invalid json",
			mockError:   nil,
			expectError: true,
		},
		{
			name:   "empty prompt",
			prompt: "",
			mockOutput: `{"type":"thread.started","thread_id":"thread_456"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"Please provide a prompt."}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Please provide a prompt.",
			expectedSession: "thread_456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for logs
			tmpDir, err := os.MkdirTemp("", "codex_test_logs_*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Set up mock client
			mockClient := &services.MockCodexClient{
				StartNewSessionFunc: func(prompt string, options *clients.CodexOptions) (string, error) {
					if prompt != tt.prompt {
						t.Errorf("Expected prompt %s, got %s", tt.prompt, prompt)
					}
					return tt.mockOutput, tt.mockError
				},
			}

			service := NewCodexService(mockClient, tmpDir, "")

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

func TestCodexService_StartNewConversationWithOptions(t *testing.T) {
	tests := []struct {
		name            string
		prompt          string
		options         *clients.CodexOptions
		serviceModel    string
		mockOutput      string
		mockError       error
		expectError     bool
		expectedOutput  string
		expectedSession string
		verifyOptions   func(*testing.T, *clients.CodexOptions)
	}{
		{
			name:   "options with custom model",
			prompt: "Test prompt",
			options: &clients.CodexOptions{
				Model: "custom-model",
			},
			serviceModel: "",
			mockOutput: `{"type":"thread.started","thread_id":"thread_opt1"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"Response with custom model"}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Response with custom model",
			expectedSession: "thread_opt1",
		},
		{
			name:         "service model overrides options model",
			prompt:       "Test prompt",
			options:      &clients.CodexOptions{Model: "option-model"},
			serviceModel: "service-model",
			mockOutput: `{"type":"thread.started","thread_id":"thread_opt2"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"Response with service model"}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Response with service model",
			expectedSession: "thread_opt2",
			verifyOptions: func(t *testing.T, opts *clients.CodexOptions) {
				if opts.Model != "service-model" {
					t.Errorf("Expected model to be overridden to 'service-model', got '%s'", opts.Model)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for logs
			tmpDir, err := os.MkdirTemp("", "codex_test_logs_*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Set up mock client
			mockClient := &services.MockCodexClient{
				StartNewSessionFunc: func(prompt string, options *clients.CodexOptions) (string, error) {
					if prompt != tt.prompt {
						t.Errorf("Expected prompt %s, got %s", tt.prompt, prompt)
					}
					if tt.verifyOptions != nil {
						tt.verifyOptions(t, options)
					}
					return tt.mockOutput, tt.mockError
				},
			}

			service := NewCodexService(mockClient, tmpDir, tt.serviceModel)

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

func TestCodexService_StartNewConversationWithSystemPrompt(t *testing.T) {
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
			mockOutput: `{"type":"thread.started","thread_id":"thread_sys1"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"Hello! I'm here to help."}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Hello! I'm here to help.",
			expectedSession: "thread_sys1",
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
			tmpDir, err := os.MkdirTemp("", "codex_test_logs_*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Set up mock client
			mockClient := &services.MockCodexClient{
				StartNewSessionFunc: func(prompt string, options *clients.CodexOptions) (string, error) {
					if tt.verifyPrompt != nil {
						tt.verifyPrompt(t, prompt)
					}
					return tt.mockOutput, tt.mockError
				},
			}

			service := NewCodexService(mockClient, tmpDir, "")

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

func TestCodexService_StartNewConversationWithDisallowedTools(t *testing.T) {
	// Create temporary directory for logs
	tmpDir, err := os.MkdirTemp("", "codex_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Set up mock client
	mockClient := &services.MockCodexClient{
		StartNewSessionFunc: func(prompt string, options *clients.CodexOptions) (string, error) {
			return `{"type":"thread.started","thread_id":"thread_tools"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"Response without disallowed tools"}}`, nil
		},
	}

	service := NewCodexService(mockClient, tmpDir, "")

	// Execute - Codex doesn't support disallowed tools, so this should just work normally
	result, err := service.StartNewConversationWithDisallowedTools("Test prompt", []string{"tool1", "tool2"})

	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}

	if result.Output != "Response without disallowed tools" {
		t.Errorf("Expected output %q, got %q", "Response without disallowed tools", result.Output)
	}
}

func TestCodexService_ContinueConversation(t *testing.T) {
	tests := []struct {
		name            string
		threadID        string
		prompt          string
		mockOutput      string
		mockError       error
		expectError     bool
		expectedOutput  string
		expectedSession string
	}{
		{
			name:     "successful conversation continue",
			threadID: "thread_123",
			prompt:   "How are you?",
			mockOutput: `{"type":"thread.started","thread_id":"thread_123"}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"I'm doing well, thank you!"}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "I'm doing well, thank you!",
			expectedSession: "thread_123",
		},
		{
			name:        "client returns error",
			threadID:    "thread_123",
			prompt:      "How are you?",
			mockOutput:  "",
			mockError:   fmt.Errorf("thread not found"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for logs
			tmpDir, err := os.MkdirTemp("", "codex_test_logs_*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Set up mock client
			mockClient := &services.MockCodexClient{
				ContinueSessionFunc: func(threadID, prompt string, options *clients.CodexOptions) (string, error) {
					if threadID != tt.threadID {
						t.Errorf("Expected threadID %s, got %s", tt.threadID, threadID)
					}
					if prompt != tt.prompt {
						t.Errorf("Expected prompt %s, got %s", tt.prompt, prompt)
					}
					return tt.mockOutput, tt.mockError
				},
			}

			service := NewCodexService(mockClient, tmpDir, "")

			// Execute
			result, err := service.ContinueConversation(tt.threadID, tt.prompt)

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

func TestCodexService_ContinueConversationWithOptions(t *testing.T) {
	tests := []struct {
		name            string
		threadID        string
		prompt          string
		options         *clients.CodexOptions
		serviceModel    string
		mockOutput      string
		mockError       error
		expectError     bool
		expectedOutput  string
		expectedSession string
	}{
		{
			name:     "continue with custom options",
			threadID: "thread_opt1",
			prompt:   "Continue prompt",
			options: &clients.CodexOptions{
				Model: "custom-model",
			},
			serviceModel: "",
			mockOutput: `{"type":"thread.started","thread_id":"thread_opt1"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"Continued response"}}`,
			mockError:       nil,
			expectError:     false,
			expectedOutput:  "Continued response",
			expectedSession: "thread_opt1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for logs
			tmpDir, err := os.MkdirTemp("", "codex_test_logs_*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			// Set up mock client
			mockClient := &services.MockCodexClient{
				ContinueSessionFunc: func(threadID, prompt string, options *clients.CodexOptions) (string, error) {
					if threadID != tt.threadID {
						t.Errorf("Expected threadID %s, got %s", tt.threadID, threadID)
					}
					if prompt != tt.prompt {
						t.Errorf("Expected prompt %s, got %s", tt.prompt, prompt)
					}
					return tt.mockOutput, tt.mockError
				},
			}

			service := NewCodexService(mockClient, tmpDir, tt.serviceModel)

			// Execute
			result, err := service.ContinueConversationWithOptions(tt.threadID, tt.prompt, tt.options)

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

func TestCodexService_CleanupOldLogs(t *testing.T) {
	mockClient := &services.MockCodexClient{}
	tmpDir, err := os.MkdirTemp("", "codex_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	service := NewCodexService(mockClient, tmpDir, "")

	// Create some test log files with different ages
	now := time.Now()
	oldTime := now.AddDate(0, 0, -10)   // 10 days ago
	recentTime := now.AddDate(0, 0, -3) // 3 days ago

	oldLogFile := filepath.Join(tmpDir, "codex-session-20240101-120000.log")
	recentLogFile := filepath.Join(tmpDir, "codex-session-20240110-120000.log")
	nonCodexFile := filepath.Join(tmpDir, "other-file.log")

	// Create test files
	if err := os.WriteFile(oldLogFile, []byte("old log"), 0644); err != nil {
		t.Fatalf("Failed to create old log file: %v", err)
	}
	if err := os.WriteFile(recentLogFile, []byte("recent log"), 0644); err != nil {
		t.Fatalf("Failed to create recent log file: %v", err)
	}
	if err := os.WriteFile(nonCodexFile, []byte("other file"), 0644); err != nil {
		t.Fatalf("Failed to create non-codex file: %v", err)
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

	// Verify non-codex file still exists
	if _, err := os.Stat(nonCodexFile); err != nil {
		t.Errorf("Non-codex file should still exist")
	}
}

func TestCodexService_CleanupOldLogs_InvalidMaxAge(t *testing.T) {
	mockClient := &services.MockCodexClient{}
	tmpDir, err := os.MkdirTemp("", "codex_test_logs_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	service := NewCodexService(mockClient, tmpDir, "")

	// Test invalid maxAgeDays values
	invalidValues := []int{0, -1, -10}
	for _, maxAge := range invalidValues {
		err := service.CleanupOldLogs(maxAge)
		if err == nil {
			t.Errorf("Expected error for maxAgeDays=%d, but got none", maxAge)
		}
	}
}

func TestCodexService_CleanupOldLogs_NonExistentDirectory(t *testing.T) {
	mockClient := &services.MockCodexClient{}
	service := NewCodexService(mockClient, "/non/existent/directory", "")

	// Should not return error for non-existent directory (it's a no-op)
	err := service.CleanupOldLogs(7)
	if err != nil {
		t.Errorf("Expected no error for non-existent directory, but got: %v", err)
	}
}

func TestCodexService_AgentName(t *testing.T) {
	mockClient := &services.MockCodexClient{}
	service := NewCodexService(mockClient, "/tmp", "")

	agentName := service.AgentName()
	expectedName := "codex"

	if agentName != expectedName {
		t.Errorf("Expected agent name %q, got %q", expectedName, agentName)
	}
}

