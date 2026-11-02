package handlers

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/gammazero/workerpool"

	"nairid/models"
)

// A message that arrived just before a restart is persisted both as job state and as seen.
// Recovery must still run it instead of dropping it as a duplicate.
func TestRecoverJobs_RunsMessagesAlreadyMarkedSeen(t *testing.T) {
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	now := time.Now()
	if err := appState.UpdateJobData("j1", models.JobData{
		JobID:              "j1",
		ClaudeSessionID:    "sess_1",
		LastMessage:        "hello",
		ProcessedMessageID: "cmsg_1",
		Status:             models.JobStatusInProgress,
		UpdatedAt:          now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := appState.UpdateJobData("j2", models.JobData{
		JobID:              "j2",
		ProcessedMessageID: "cmsg_2",
		Status:             models.JobStatusInProgress,
		UpdatedAt:          now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := appState.AddQueuedMessage(models.QueuedMessage{
		ProcessedMessageID: "cmsg_3",
		JobID:              "j2",
		MessageType:        models.MessageTypeUserMessage,
		Message:            "and again",
		QueuedAt:           now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"cmsg_1", "cmsg_2", "cmsg_3"} {
		if err := appState.MarkMessageSeen(id, now.Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
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
