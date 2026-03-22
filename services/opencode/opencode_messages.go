package opencode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"
)

// OpenCodeMessage represents a simplified message interface for OpenCode
type OpenCodeMessage interface {
	GetType() string
	GetSessionID() string
}

// OpenCodeTextMessage represents a text message from OpenCode containing the response
type OpenCodeTextMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Part      struct {
		Text string `json:"text"`
	} `json:"part"`
}

func (t OpenCodeTextMessage) GetType() string {
	return t.Type
}

func (t OpenCodeTextMessage) GetSessionID() string {
	return t.SessionID
}

// OpenCodeStepMessage represents step_start or step_finish messages
type OpenCodeStepMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
}

func (s OpenCodeStepMessage) GetType() string {
	return s.Type
}

func (s OpenCodeStepMessage) GetSessionID() string {
	return s.SessionID
}

// UnknownOpenCodeMessage represents an unknown message type from OpenCode
type UnknownOpenCodeMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
}

func (u UnknownOpenCodeMessage) GetType() string {
	return u.Type
}

func (u UnknownOpenCodeMessage) GetSessionID() string {
	return u.SessionID
}

// OpenCodeToolUseMessage represents a tool_use message from OpenCode
// This captures tool calls made by the model (read, edit, bash, etc.)
type OpenCodeToolUseMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Part      struct {
		Tool  string `json:"tool"`
		State struct {
			Status string `json:"status"`
			Title  string `json:"title"`
			Input  struct {
				FilePath  string `json:"filePath"`
				OldString string `json:"oldString"`
				NewString string `json:"newString"`
			} `json:"input"`
			Metadata struct {
				Diff     string `json:"diff"`
				FileDiff struct {
					File      string `json:"file"`
					Additions int    `json:"additions"`
					Deletions int    `json:"deletions"`
				} `json:"filediff"`
			} `json:"metadata"`
		} `json:"state"`
	} `json:"part"`
}

func (t OpenCodeToolUseMessage) GetType() string {
	return t.Type
}

func (t OpenCodeToolUseMessage) GetSessionID() string {
	return t.SessionID
}

// OpenCodeErrorMessage represents a JSON error message from OpenCode (type: "error")
// This happens when OpenCode encounters an API error (e.g. invalid API key, insufficient balance,
// model not found) and emits a structured error JSON line.
type OpenCodeErrorMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Error     struct {
		Name string `json:"name"`
		Data struct {
			Message    string `json:"message"`
			StatusCode int    `json:"statusCode"`
		} `json:"data"`
	} `json:"error"`
}

func (e OpenCodeErrorMessage) GetType() string {
	return e.Type
}

func (e OpenCodeErrorMessage) GetSessionID() string {
	return e.SessionID
}

// OpenCodeRawErrorMessage represents a non-JSON error output from OpenCode
// This happens when opencode itself crashes or encounters a startup error
type OpenCodeRawErrorMessage struct {
	RawOutput string
}

func (e OpenCodeRawErrorMessage) GetType() string {
	return "raw_error"
}

func (e OpenCodeRawErrorMessage) GetSessionID() string {
	return ""
}

// MapOpenCodeOutputToMessages parses OpenCode command output into structured messages
func MapOpenCodeOutputToMessages(output string) ([]OpenCodeMessage, error) {
	var messages []OpenCodeMessage

	// Use a scanner with a larger buffer to handle long lines
	scanner := bufio.NewScanner(strings.NewReader(output))
	// Set a 5MB buffer to handle long JSON lines
	scanner.Buffer(make([]byte, 0, 5*1024*1024), 5*1024*1024)

	hasJSONLines := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "{") {
			// JSON line — parse normally
			hasJSONLines = true
			message := parseOpenCodeMessage([]byte(line))
			messages = append(messages, message)
		}
		// Non-JSON lines (e.g. "Performing one time database migration...") are skipped.
		// If no JSON lines are found at all, the entire output is treated as a raw error below.
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// If we found JSON lines, return them (non-JSON preamble lines are ignored)
	if hasJSONLines {
		return messages, nil
	}

	// No JSON lines found — treat the entire output as a raw error
	trimmedOutput := strings.TrimSpace(output)
	if trimmedOutput != "" {
		return []OpenCodeMessage{
			OpenCodeRawErrorMessage{RawOutput: trimmedOutput},
		}, nil
	}

	return messages, nil
}

// parseOpenCodeMessage attempts to parse a JSON line into the appropriate message type
func parseOpenCodeMessage(lineBytes []byte) OpenCodeMessage {
	// First, extract just the type to determine which struct to use
	var typeCheck struct {
		Type string `json:"type"`
	}

	if err := json.Unmarshal(lineBytes, &typeCheck); err != nil {
		// If we can't even parse the type, return unknown message
		return UnknownOpenCodeMessage{
			Type:      "unknown",
			SessionID: "",
		}
	}

	// Parse based on type
	switch typeCheck.Type {
	case "text":
		var textMsg OpenCodeTextMessage
		if err := json.Unmarshal(lineBytes, &textMsg); err == nil {
			return textMsg
		}

	case "step_start", "step_finish":
		var stepMsg OpenCodeStepMessage
		if err := json.Unmarshal(lineBytes, &stepMsg); err == nil {
			return stepMsg
		}

	case "tool_use":
		var toolMsg OpenCodeToolUseMessage
		if err := json.Unmarshal(lineBytes, &toolMsg); err == nil {
			return toolMsg
		}

	case "error":
		var errMsg OpenCodeErrorMessage
		if err := json.Unmarshal(lineBytes, &errMsg); err == nil {
			return errMsg
		}
	}

	// For all other types, extract basic info for unknown message
	var unknownMsg struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionID"`
	}

	if err := json.Unmarshal(lineBytes, &unknownMsg); err == nil {
		return UnknownOpenCodeMessage{
			Type:      unknownMsg.Type,
			SessionID: unknownMsg.SessionID,
		}
	}

	// Return default unknown message
	return UnknownOpenCodeMessage{
		Type:      "unknown",
		SessionID: "",
	}
}

// ExtractOpenCodeSessionID extracts session ID from OpenCode messages
func ExtractOpenCodeSessionID(messages []OpenCodeMessage) string {
	// Session ID is present in all messages, take from the first one
	if len(messages) > 0 {
		sessionID := messages[0].GetSessionID()
		if sessionID != "" {
			return sessionID
		}
	}
	return "unknown"
}

// ExtractOpenCodeResult extracts the result text from OpenCode messages
func ExtractOpenCodeResult(messages []OpenCodeMessage) (string, error) {
	// Check for error messages first
	for _, msg := range messages {
		// JSON error messages (type: "error") — API errors like invalid key, insufficient balance
		if errMsg, ok := msg.(OpenCodeErrorMessage); ok {
			errorDetail := errMsg.Error.Data.Message
			if errorDetail == "" {
				errorDetail = errMsg.Error.Name
			}
			if errorDetail == "" {
				errorDetail = "unknown error"
			}
			return "", fmt.Errorf("opencode API error: %s", errorDetail)
		}
		// Raw error messages — opencode crashed or failed to start (non-JSON output)
		if rawErr, ok := msg.(OpenCodeRawErrorMessage); ok {
			errorSummary := extractErrorSummary(rawErr.RawOutput)
			return "", fmt.Errorf("opencode error: %s", errorSummary)
		}
	}

	// Collect all text messages and concatenate their content
	var textParts []string
	// Also collect tool use summaries as fallback
	var toolSummaries []string

	for _, msg := range messages {
		if textMsg, ok := msg.(OpenCodeTextMessage); ok {
			if textMsg.Part.Text != "" {
				textParts = append(textParts, textMsg.Part.Text)
			}
		}
		// Collect completed tool operations for fallback summary
		if toolMsg, ok := msg.(OpenCodeToolUseMessage); ok {
			if toolMsg.Part.State.Status == "completed" {
				summary := extractToolSummary(toolMsg)
				if summary != "" {
					toolSummaries = append(toolSummaries, summary)
				}
			}
		}
	}

	// Prefer text messages if available
	if len(textParts) > 0 {
		return strings.Join(textParts, "\n\n"), nil
	}

	// Fall back to tool summaries if no text messages
	// This handles cases where the model completed work via tool calls
	// but didn't emit a final text response
	if len(toolSummaries) > 0 {
		return "Completed: " + strings.Join(toolSummaries, "; "), nil
	}

	return "", fmt.Errorf("no text message found")
}

// extractToolSummary generates a human-readable summary from a tool use message
func extractToolSummary(toolMsg OpenCodeToolUseMessage) string {
	tool := toolMsg.Part.Tool
	state := toolMsg.Part.State

	switch tool {
	case "edit":
		// For edit operations, show file and diff stats
		title := state.Title
		if title == "" {
			title = state.Input.FilePath
		}
		if title == "" {
			return ""
		}

		additions := state.Metadata.FileDiff.Additions
		deletions := state.Metadata.FileDiff.Deletions

		if additions > 0 || deletions > 0 {
			return fmt.Sprintf("edited %s (+%d/-%d lines)", title, additions, deletions)
		}
		return fmt.Sprintf("edited %s", title)

	case "write":
		// For write operations, show file created
		title := state.Title
		if title == "" {
			title = state.Input.FilePath
		}
		if title != "" {
			return fmt.Sprintf("created %s", title)
		}

	case "bash":
		// For bash operations, just note that a command was run
		return "ran command"
	}

	// For other tools (read, glob, grep, etc.), we don't include them
	// as they are informational rather than actions
	return ""
}

// extractErrorSummary extracts a meaningful error summary from raw opencode error output
// It looks for common error patterns like "Error:", exception names, etc.
func extractErrorSummary(rawOutput string) string {
	lines := strings.Split(rawOutput, "\n")

	// Look for lines containing common error indicators
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		// Look for JavaScript/TypeScript error patterns
		if strings.Contains(trimmedLine, "Error:") {
			// Return the error line, but truncate if too long
			if len(trimmedLine) > 200 {
				return trimmedLine[:200] + "..."
			}
			return trimmedLine
		}
	}

	// If no specific error line found, return first non-empty line truncated
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine != "" {
			if len(trimmedLine) > 200 {
				return trimmedLine[:200] + "..."
			}
			return trimmedLine
		}
	}

	// Fallback
	if len(rawOutput) > 200 {
		return rawOutput[:200] + "..."
	}
	return rawOutput
}
