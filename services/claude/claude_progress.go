package claude

import (
	"encoding/json"
	"fmt"
	"nairid/models"
)

// toolMeta stores metadata about a pending tool_use call
type toolMeta struct {
	name  string
	input string
}

// ClaudeProgressTracker is a stateful mapper that pairs tool_use events with their tool_result events.
// It is NOT safe for concurrent use — it is designed to be called sequentially from a single stdout reader goroutine.
type ClaudeProgressTracker struct {
	pendingTools map[string]toolMeta
}

// NewClaudeProgressTracker creates a new tracker for pairing tool_use/tool_result events.
func NewClaudeProgressTracker() *ClaudeProgressTracker {
	return &ClaudeProgressTracker{
		pendingTools: make(map[string]toolMeta),
	}
}

// MapLine maps a single NDJSON line from Claude Code to a progress payload,
// tracking tool_use IDs to pair them with their corresponding tool_result events.
func (t *ClaudeProgressTracker) MapLine(line []byte) *models.AgentProgressPayload {
	var typeCheck struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype,omitempty"`
	}
	if err := json.Unmarshal(line, &typeCheck); err != nil {
		return nil
	}

	switch typeCheck.Type {
	case "assistant":
		return t.mapClaudeAssistant(line)
	case "user":
		return t.mapClaudeUser(line)
	case "tool_progress":
		return mapClaudeToolProgress(line)
	case "system":
		return mapClaudeSystem(line, typeCheck.Subtype)
	default:
		return nil
	}
}

func (t *ClaudeProgressTracker) mapClaudeAssistant(line []byte) *models.AgentProgressPayload {
	var msg struct {
		ParentToolUseID string `json:"parent_tool_use_id"`
		Message         struct {
			Content []json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil
	}

	for _, raw := range msg.Message.Content {
		var block struct {
			Type  string          `json:"type"`
			ID    string          `json:"id,omitempty"`
			Text  string          `json:"text,omitempty"`
			Name  string          `json:"name,omitempty"`
			Input json.RawMessage `json:"input,omitempty"`
		}
		if err := json.Unmarshal(raw, &block); err != nil {
			continue
		}

		switch block.Type {
		case "tool_use":
			toolInput := summarizeToolInput(block.Name, block.Input)
			if block.ID != "" {
				t.pendingTools[block.ID] = toolMeta{name: block.Name, input: toolInput}
			}
			return &models.AgentProgressPayload{
				ProgressType:    models.ProgressTypeToolUse,
				ToolName:        block.Name,
				ToolInput:       toolInput,
				ToolStatus:      "running",
				ToolUseID:       block.ID,
				ParentToolUseID: msg.ParentToolUseID,
			}
		case "text":
			if block.Text != "" {
				return &models.AgentProgressPayload{
					ProgressType:    models.ProgressTypeText,
					TextDelta:       block.Text,
					ParentToolUseID: msg.ParentToolUseID,
				}
			}
		}
	}

	return nil
}

func (t *ClaudeProgressTracker) mapClaudeUser(line []byte) *models.AgentProgressPayload {
	var msg struct {
		ParentToolUseID string `json:"parent_tool_use_id"`
		Message         struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil
	}

	// Check if this is a tool result (array of tool_result blocks)
	var blocks []struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id,omitempty"`
		IsError   bool            `json:"is_error,omitempty"`
		Content   json.RawMessage `json:"content,omitempty"`
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

			payload := &models.AgentProgressPayload{
				ProgressType:    models.ProgressTypeToolUse,
				ToolStatus:      status,
				ToolUseID:       block.ToolUseID,
				ToolOutput:      extractToolOutput(block.Content),
				ParentToolUseID: msg.ParentToolUseID,
			}

			// Look up the pending tool_use to populate name and input
			if block.ToolUseID != "" {
				if meta, ok := t.pendingTools[block.ToolUseID]; ok {
					payload.ToolName = meta.name
					payload.ToolInput = meta.input
					delete(t.pendingTools, block.ToolUseID)
				}
			}

			return payload
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
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil
		}
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeSubagent,
			Summary:      "Subagent started: " + msg.Description,
			ToolStatus:   "running",
		}
	case "task_progress":
		var msg struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil
		}
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeSubagent,
			Summary:      msg.Description,
			ToolStatus:   "running",
		}
	case "task_notification":
		var msg struct {
			Status  string `json:"status"`
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil
		}
		toolStatus := "completed"
		if msg.Status == "failed" || msg.Status == "stopped" {
			toolStatus = "error"
		}
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeSubagent,
			Summary:      msg.Summary,
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
		return extractString(fields, "command")
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
				return v
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

// extractToolOutput extracts text content from a tool_result content field.
// Content can be a JSON string or an array of content blocks with "text" type.
func extractToolOutput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try as a plain string first
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	// Try as array of content blocks
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}

	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			return b.Text
		}
	}

	return ""
}

