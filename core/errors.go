package core

import (
	"errors"
	"fmt"
	"time"
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

// ErrSessionTimeout is a CLI run that nairid killed because it hit the session timeout.
// SessionID is the session the CLI had printed, so the next turn can resume it.
type ErrSessionTimeout struct {
	Timeout   time.Duration
	SessionID string
	Err       error
}

func (e *ErrSessionTimeout) Error() string {
	return fmt.Sprintf("session timed out after %s: %v", e.Timeout, e.Err)
}

func (e *ErrSessionTimeout) Unwrap() error {
	return e.Err
}

func (e *ErrSessionTimeout) WithSessionID(sessionID string) *ErrSessionTimeout {
	withID := *e
	withID.SessionID = sessionID
	return &withID
}

// UserMessage is what the chat thread shows. It never quotes the agent's output: a run cut
// off mid-stream would otherwise show its last progress sentence as the error.
func (e *ErrSessionTimeout) UserMessage() string {
	return fmt.Sprintf(
		"nairid stopped this run after it hit the %s session timeout. Raise it in the agent's Advanced settings.",
		FormatTimeout(e.Timeout),
	)
}

func IsSessionTimeout(err error) (*ErrSessionTimeout, bool) {
	var timeoutErr *ErrSessionTimeout
	if errors.As(err, &timeoutErr) {
		return timeoutErr, true
	}
	return nil, false
}

// FormatTimeout renders 1h as "1h" and 90m as "1h30m", rather than time.Duration's "1h0m0s".
func FormatTimeout(d time.Duration) string {
	if d%time.Minute != 0 {
		return d.Round(time.Second).String()
	}
	hours := d / time.Hour
	minutes := (d % time.Hour) / time.Minute
	if hours == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	if minutes == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%dm", hours, minutes)
}
