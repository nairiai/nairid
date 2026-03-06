package services

import (
	"encoding/json"
	"fmt"
	"nairid/models"
)

// MapClaudeLineToProgress maps a single NDJSON line from Claude Code to a progress payload.
// Returns nil if the line is not progress-relevant (system init, result, etc.).
func MapClaudeLineToProgress(line []byte) *models.AgentProgressPayload {
	var typeCheck struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype,omitempty"`
	}
	if err := json.Unmarshal(line, &typeCheck); err != nil {
		return nil
	}

	switch typeCheck.Type {
	case "assistant":
		return mapClaudeAssistant(line)
	case "user":
		return mapClaudeUser(line)
	case "tool_progress":
		return mapClaudeToolProgress(line)
	case "system":
		return mapClaudeSystem(line, typeCheck.Subtype)
	default:
		return nil
	}
}

func mapClaudeAssistant(line []byte) *models.AgentProgressPayload {
	var msg struct {
		Message struct {
			Content []json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil
	}

	for _, raw := range msg.Message.Content {
		var block struct {
			Type  string          `json:"type"`
			Text  string          `json:"text,omitempty"`
			Name  string          `json:"name,omitempty"`
			Input json.RawMessage `json:"input,omitempty"`
		}
		if err := json.Unmarshal(raw, &block); err != nil {
			continue
		}

		switch block.Type {
		case "tool_use":
			return &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolName:     block.Name,
				ToolInput:    summarizeToolInput(block.Name, block.Input),
				ToolStatus:   "running",
			}
		case "text":
			if block.Text != "" {
				return &models.AgentProgressPayload{
					ProgressType: models.ProgressTypeText,
					TextDelta:    truncate(block.Text, 500),
				}
			}
		}
	}

	return nil
}

func mapClaudeUser(line []byte) *models.AgentProgressPayload {
	var msg struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil
	}

	// Check if this is a tool result (array of tool_result blocks)
	var blocks []struct {
		Type    string `json:"type"`
		IsError bool   `json:"is_error,omitempty"`
	}
	if err := json.Unmarshal(msg.Message.Content, &blocks); err != nil {
		return nil
	}

	for _, block := range blocks {
		if block.Type == "tool_result" {
			status := "completed"
			if block.IsError {
				status = "error"
			}
			return &models.AgentProgressPayload{
				ProgressType: models.ProgressTypeToolUse,
				ToolStatus:   status,
			}
		}
	}

	return nil
}

func mapClaudeToolProgress(line []byte) *models.AgentProgressPayload {
	var msg struct {
		ToolName           string  `json:"tool_name"`
		ElapsedTimeSeconds float64 `json:"elapsed_time_seconds"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil
	}

	return &models.AgentProgressPayload{
		ProgressType: models.ProgressTypeToolHeartbeat,
		ToolName:     msg.ToolName,
		Summary:      fmt.Sprintf("Running for %.0fs...", msg.ElapsedTimeSeconds),
		ToolStatus:   "running",
	}
}

func mapClaudeSystem(line []byte, subtype string) *models.AgentProgressPayload {
	switch subtype {
	case "task_started":
		var msg struct {
			Description string `json:"description"`
		}
		json.Unmarshal(line, &msg)
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeSubagent,
			Summary:      "Subagent started: " + truncate(msg.Description, 200),
			ToolStatus:   "running",
		}
	case "task_progress":
		var msg struct {
			Description string `json:"description"`
		}
		json.Unmarshal(line, &msg)
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeSubagent,
			Summary:      truncate(msg.Description, 200),
			ToolStatus:   "running",
		}
	case "task_notification":
		var msg struct {
			Status  string `json:"status"`
			Summary string `json:"summary"`
		}
		json.Unmarshal(line, &msg)
		toolStatus := "completed"
		if msg.Status == "failed" || msg.Status == "stopped" {
			toolStatus = "error"
		}
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeSubagent,
			Summary:      truncate(msg.Summary, 200),
			ToolStatus:   toolStatus,
		}
	default:
		return nil
	}
}

// summarizeToolInput creates a short summary from tool input JSON
func summarizeToolInput(toolName string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return ""
	}

	// Extract the most relevant field based on tool name
	switch toolName {
	case "Read":
		return extractString(fields, "file_path")
	case "Write":
		return extractString(fields, "file_path")
	case "Edit":
		return extractString(fields, "file_path")
	case "Bash":
		return truncate(extractString(fields, "command"), 100)
	case "Grep":
		return extractString(fields, "pattern")
	case "Glob":
		return extractString(fields, "pattern")
	case "WebFetch":
		return extractString(fields, "url")
	case "WebSearch":
		return extractString(fields, "query")
	case "Task":
		return extractString(fields, "description")
	default:
		// For unknown tools, try common field names
		for _, key := range []string{"file_path", "path", "command", "query", "pattern", "url"} {
			if v := extractString(fields, key); v != "" {
				return truncate(v, 100)
			}
		}
		return ""
	}
}

func extractString(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
