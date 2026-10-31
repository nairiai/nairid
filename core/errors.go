package core

import (
	"errors"
	"fmt"
)

// ClaudeParseError represents a failure to parse Claude output with error log file path
type ClaudeParseError struct {
	Message     string
	LogFilePath string
	OriginalErr error
}

func (e *ClaudeParseError) Error() string {
	return e.Message
}

// IsClaudeParseError checks if an error is a ClaudeParseError
func IsClaudeParseError(err error) (*ClaudeParseError, bool) {
	parseErr, ok := err.(*ClaudeParseError)
	return parseErr, ok
}

// ErrClaudeCommandErr represents an error from an agent CLI command that includes the command output
type ErrClaudeCommandErr struct {
	Agent  string // CLI that failed, e.g. "Codex"; names the right tool in the user-facing error
	Err    error  // The original command error
	Output string // The CLI output (may contain JSON response)
}

func (e *ErrClaudeCommandErr) Error() string {
	agent := e.Agent
	if agent == "" {
		agent = "agent"
	}
	return fmt.Sprintf("%s command failed: %v\nOutput: %s", agent, e.Err, e.Output)
}

func (e *ErrClaudeCommandErr) Unwrap() error {
	return e.Err
}

// IsClaudeCommandErr checks if an error is a Claude command error
func IsClaudeCommandErr(err error) (*ErrClaudeCommandErr, bool) {
	var claudeErr *ErrClaudeCommandErr
	if errors.As(err, &claudeErr) {
		return claudeErr, true
	}
	return nil, false
}

// ErrClaudeCLISuccessfulResponse represents a case where the Claude CLI exited with
// a non-zero code, but the JSON response indicates success (is_error: false).
// This can happen due to Claude CLI bugs during finalization/cleanup.
// The caller should treat this as a successful response and extract the result.
type ErrClaudeCLISuccessfulResponse struct {
	Result    string // The successful response text
	SessionID string // The session ID from the response
}

func (e *ErrClaudeCLISuccessfulResponse) Error() string {
	return fmt.Sprintf("CLI exited non-zero but response was successful: %s", e.Result)
}

// IsClaudeCLISuccessfulResponse checks if an error is actually a successful response
// that was incorrectly flagged due to CLI exit code issues
func IsClaudeCLISuccessfulResponse(err error) (*ErrClaudeCLISuccessfulResponse, bool) {
	var successErr *ErrClaudeCLISuccessfulResponse
	if errors.As(err, &successErr) {
		return successErr, true
	}
	return nil, false
}

// ErrTurnStopped ends a turn that a stop request cut short. SessionID is what the CLI
// had printed by then, empty when it never got that far.
type ErrTurnStopped struct {
	SessionID string
}

func (e *ErrTurnStopped) Error() string {
	return "turn stopped before the agent finished"
}

func IsTurnStopped(err error) (*ErrTurnStopped, bool) {
	var stopped *ErrTurnStopped
	if errors.As(err, &stopped) {
		return stopped, true
	}
	return nil, false
}
