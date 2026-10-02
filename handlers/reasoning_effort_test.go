package handlers

import (
	"testing"

	"nairid/models"
)

func TestResolveReasoningEffort(t *testing.T) {
	tests := []struct {
		name         string
		level        string
		checkedModel string
		ownModel     string
		want         string
	}{
		{"empty level is model default", "", "claude-opus-4-8", "claude-opus-4-8", ""},
		{"default keyword is model default", "default", "claude-opus-4-8", "claude-opus-4-8", ""},
		{"matching model applies level", "high", "gpt-5.5", "gpt-5.5", "high"},
		{"max applies", "max", "opencode/claude-opus-5-5", "opencode/claude-opus-5-5", "max"},
		{"unknown level dropped", "turbo", "gpt-5.5", "gpt-5.5", ""},
		{"wrong case dropped", "High", "gpt-5.5", "gpt-5.5", ""},
		{"model mismatch dropped", "max", "claude-opus-4-8", "gpt-5.5", ""},
		{"no own model dropped", "high", "claude-opus-4-8", "", ""},
		{"no checked model dropped", "high", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveReasoningEffort(tt.level, tt.checkedModel, tt.ownModel); got != tt.want {
				t.Fatalf("resolveReasoningEffort(%q, %q, %q) = %q, want %q", tt.level, tt.checkedModel, tt.ownModel, got, tt.want)
			}
		})
	}
}

// A reply can start the conversation over: the first turn failed before it had a
// session, or the session was lost. It must run at the level the conversation has.
func TestStartPayloadForUnstartedJob(t *testing.T) {
	reply := models.UserMessagePayload{
		JobID: "j1", Message: "try again", ProcessedMessageID: "cmsg_2", MessageLink: "https://example.slack.com/p2",
	}

	t.Run("a job whose first turn failed keeps its level and the model it was checked for", func(t *testing.T) {
		job := &models.JobData{JobID: "j1", ReasoningEffort: "high", EffortModel: "gpt-5.5"}

		got := startPayloadForUnstartedJob(reply, job)

		if got.ReasoningEffort != "high" || got.ReasoningEffortModel != "gpt-5.5" {
			t.Errorf("level = %q for model %q, want high for gpt-5.5", got.ReasoningEffort, got.ReasoningEffortModel)
		}
		if got.JobID != "j1" || got.Message != "try again" || got.ProcessedMessageID != "cmsg_2" ||
			got.MessageLink != "https://example.slack.com/p2" {
			t.Errorf("the reply was not carried over: %+v", got)
		}
	})

	t.Run("a job this agent has never seen starts at the model default", func(t *testing.T) {
		got := startPayloadForUnstartedJob(reply, nil)

		if got.ReasoningEffort != "" || got.ReasoningEffortModel != "" {
			t.Errorf("level = %q for model %q, want none", got.ReasoningEffort, got.ReasoningEffortModel)
		}
	})
}
