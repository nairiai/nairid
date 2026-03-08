package codex

import (
	"encoding/json"
	"fmt"
	"nairid/models"
)

// MapCodexLineToProgress maps a single NDJSON line from Codex to a progress payload.
// Returns nil if the line is not progress-relevant.
func MapCodexLineToProgress(line []byte) *models.AgentProgressPayload {
	var typeCheck struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &typeCheck); err != nil {
		return nil
	}

	switch typeCheck.Type {
	case "item.completed":
		return mapCodexItem(line, "completed")
	case "item.updated":
		return mapCodexItem(line, "running")
	case "turn.failed":
		return mapCodexTurnFailed(line)
	case "error":
		var msg struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil
		}
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeStep,
			Summary:      msg.Message,
			ToolStatus:   "error",
		}
	default:
		return nil
	}
}

func mapCodexItem(line []byte, defaultStatus string) *models.AgentProgressPayload {
	var msg struct {
		Item struct {
			Type   string `json:"type"`
			Text   string `json:"text,omitempty"`
			Status string `json:"status,omitempty"`
		} `json:"item"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil
	}

	switch msg.Item.Type {
	case "command_execution":
		status := defaultStatus
		if msg.Item.Status != "" {
			status = msg.Item.Status
		}
		var detail struct {
			Item struct {
				Command string `json:"command"`
			} `json:"item"`
		}
		if err := json.Unmarshal(line, &detail); err != nil {
			return nil
		}
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeToolUse,
			ToolName:     "command_execution",
			ToolInput:    detail.Item.Command,
			ToolStatus:   status,
		}

	case "file_change":
		var detail struct {
			Item struct {
				FilePath string `json:"file_path"`
			} `json:"item"`
		}
		if err := json.Unmarshal(line, &detail); err != nil {
			return nil
		}
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeToolUse,
			ToolName:     "file_change",
			ToolInput:    detail.Item.FilePath,
			ToolStatus:   defaultStatus,
		}

	case "mcp_tool_call":
		var detail struct {
			Item struct {
				ServerName string `json:"server_name"`
				ToolName   string `json:"tool_name"`
			} `json:"item"`
		}
		if err := json.Unmarshal(line, &detail); err != nil {
			return nil
		}
		toolName := fmt.Sprintf("mcp:%s/%s", detail.Item.ServerName, detail.Item.ToolName)
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeToolUse,
			ToolName:     toolName,
			ToolStatus:   defaultStatus,
		}

	case "web_search":
		var detail struct {
			Item struct {
				Query string `json:"query"`
			} `json:"item"`
		}
		if err := json.Unmarshal(line, &detail); err != nil {
			return nil
		}
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeToolUse,
			ToolName:     "web_search",
			ToolInput:    detail.Item.Query,
			ToolStatus:   defaultStatus,
		}

	case "agent_message":
		if msg.Item.Text == "" {
			return nil
		}
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeText,
			TextDelta:    msg.Item.Text,
		}

	case "reasoning":
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeStep,
			Summary:      "Reasoning...",
		}

	case "todo_list":
		var detail struct {
			Item struct {
				Items []struct {
					Text      string `json:"text"`
					Completed bool   `json:"completed"`
				} `json:"items"`
			} `json:"item"`
		}
		if err := json.Unmarshal(line, &detail); err != nil {
			return nil
		}
		total := len(detail.Item.Items)
		done := 0
		for _, item := range detail.Item.Items {
			if item.Completed {
				done++
			}
		}
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeStep,
			Summary:      fmt.Sprintf("Updated checklist: %d/%d items done", done, total),
		}

	case "collab_tool_call":
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeSubagent,
			Summary:      msg.Item.Text,
			ToolStatus:   defaultStatus,
		}

	case "error":
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeToolUse,
			ToolName:     "error",
			ToolStatus:   "error",
			Summary:      msg.Item.Text,
		}

	default:
		return nil
	}
}

// mapCodexTurnFailed handles the turn.failed event where the error field
// can be either a plain string or an object with a "message" field.
func mapCodexTurnFailed(line []byte) *models.AgentProgressPayload {
	// Try error as a string first
	var strMsg struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &strMsg); err == nil && strMsg.Error != "" {
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeStep,
			Summary:      fmt.Sprintf("Turn failed: %s", strMsg.Error),
			ToolStatus:   "error",
		}
	}

	// Try error as an object with a message field
	var objMsg struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &objMsg); err == nil && objMsg.Error.Message != "" {
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeStep,
			Summary:      fmt.Sprintf("Turn failed: %s", objMsg.Error.Message),
			ToolStatus:   "error",
		}
	}

	return &models.AgentProgressPayload{
		ProgressType: models.ProgressTypeStep,
		Summary:      "Turn failed",
		ToolStatus:   "error",
	}
}

