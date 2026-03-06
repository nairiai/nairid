package opencode

import (
	"encoding/json"
	"fmt"
	"nairid/models"
)

// MapOpenCodeLineToProgress maps a single NDJSON line from OpenCode to a progress payload.
// Returns nil if the line is not progress-relevant.
func MapOpenCodeLineToProgress(line []byte) *models.AgentProgressPayload {
	var typeCheck struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &typeCheck); err != nil {
		return nil
	}

	switch typeCheck.Type {
	case "tool_use":
		return mapOpenCodeToolUse(line)
	case "text":
		return mapOpenCodeText(line)
	case "step_start":
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeStep,
			Summary:      "Step started",
		}
	case "step_finish":
		var msg struct {
			Part struct {
				Reason string `json:"reason"`
			} `json:"part"`
		}
		json.Unmarshal(line, &msg)
		summary := "Step completed"
		if msg.Part.Reason != "" {
			summary = fmt.Sprintf("Step completed (reason: %s)", msg.Part.Reason)
		}
		return &models.AgentProgressPayload{
			ProgressType: models.ProgressTypeStep,
			Summary:      summary,
		}
	default:
		return nil
	}
}

func mapOpenCodeToolUse(line []byte) *models.AgentProgressPayload {
	var msg struct {
		Part struct {
			Tool  string `json:"tool"`
			State struct {
				Status string `json:"status"`
				Title  string `json:"title"`
			} `json:"state"`
		} `json:"part"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil
	}

	return &models.AgentProgressPayload{
		ProgressType: models.ProgressTypeToolUse,
		ToolName:     msg.Part.Tool,
		ToolInput:    msg.Part.State.Title,
		ToolStatus:   msg.Part.State.Status,
	}
}

func mapOpenCodeText(line []byte) *models.AgentProgressPayload {
	var msg struct {
		Part struct {
			Text string `json:"text"`
		} `json:"part"`
	}
	if err := json.Unmarshal(line, &msg); err != nil || msg.Part.Text == "" {
		return nil
	}

	text := msg.Part.Text
	if len(text) > 500 {
		text = text[:500] + "..."
	}

	return &models.AgentProgressPayload{
		ProgressType: models.ProgressTypeText,
		TextDelta:    text,
	}
}
