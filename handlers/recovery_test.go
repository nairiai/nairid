package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gammazero/workerpool"

	"nairid/models"
)

// A message that arrived just before an upgrade from a version that tracked "seen" is
// persisted both as job state and as seen. Recovery must still run it, not drop it as done.
func TestRecoverJobs_RunsMessagesSeenByOlderVersion(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	now := time.Now()
	legacy := map[string]any{
		"agent_id": "agent",
		"jobs": map[string]models.JobData{
			"j1": {JobID: "j1", ClaudeSessionID: "sess_1", LastMessage: "hello", ProcessedMessageID: "cmsg_1", Status: models.JobStatusInProgress, UpdatedAt: now},
			"j2": {JobID: "j2", ProcessedMessageID: "cmsg_2", Status: models.JobStatusInProgress, UpdatedAt: now},
			"j3": {JobID: "j3", ProcessedMessageID: "cmsg_0", Status: models.JobStatusCompleted, UpdatedAt: now},
		},
		"queued_messages": map[string]models.QueuedMessage{
			"cmsg_3": {ProcessedMessageID: "cmsg_3", JobID: "j2", MessageType: models.MessageTypeUserMessage, Message: "and again", QueuedAt: now},
		},
		"seen_messages": map[string]time.Time{
			"cmsg_0": now.Add(-time.Hour), "cmsg_1": now.Add(-time.Minute), "cmsg_2": now.Add(-time.Minute), "cmsg_3": now.Add(-time.Minute),
		},
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	appState, _, err := RestoreAppState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !appState.IsMessageDone("cmsg_0") {
		t.Error("a seen message of a finished job should migrate to done")
	}

	wp := workerpool.New(1)
	defer wp.StopWait()
	dispatcher := NewJobDispatcher(nil, wp, appState)
	j1 := make(chan models.BaseMessage, 10)
	j2 := make(chan models.BaseMessage, 10)
	dispatcher.activeJobs["j1"] = j1
	dispatcher.activeJobs["j2"] = j2

	RecoverJobs(appState, nil, dispatcher, nil)

	if len(j1) != 1 {
		t.Errorf("job j1: %d messages recovered, want 1", len(j1))
	}
	if len(j2) != 2 {
		t.Errorf("job j2: %d messages recovered, want 2 (in-progress + queued)", len(j2))
	}

	// A redelivery of a recovered message is still a duplicate.
	dispatcher.Dispatch(replyMessage("1"))
	if len(j1) != 1 {
		t.Errorf("redelivered message was dispatched again, j1 has %d", len(j1))
	}
}
