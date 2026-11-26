package handlers

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func setAsideFiles(t *testing.T, statePath string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(statePath))
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), filepath.Base(statePath)+".unreadable-") {
			found = append(found, filepath.Join(filepath.Dir(statePath), entry.Name()))
		}
	}
	return found
}

func TestRestoreAppState_UnreadableFileBootsAndHoldsCleanupAcrossRestarts(t *testing.T) {
	cases := map[string][]byte{
		"garbage":      []byte("not json"),
		"empty":        {},
		"whitespace":   []byte(" \n"),
		"truncated":    []byte(`{"agent_id":"agent","jobs":{`),
		"wrong type":   []byte(`{"agent_id":"agent","jobs":5}`),
		"nul bytes":    make([]byte, 64),
		"null":         []byte("null"),
		"empty object": []byte("{}"),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(statePath, content, 0o644); err != nil {
				t.Fatal(err)
			}

			appState, agentID, err := RestoreAppState(statePath)
			if err != nil {
				t.Fatalf("restore: %v", err)
			}
			if agentID == "" || len(appState.GetAllJobs()) != 0 {
				t.Fatalf("agent %q with jobs %v, want a new agent with none", agentID, appState.GetAllJobs())
			}
			asides := setAsideFiles(t, statePath)
			if len(asides) != 1 {
				t.Fatalf("set-aside files = %v", asides)
			}
			if kept, err := os.ReadFile(asides[0]); err != nil || !bytes.Equal(kept, content) {
				t.Fatalf("set-aside file = %q, %v, want the original bytes", kept, err)
			}
			if appState.CleanupHold() == "" {
				t.Fatal("cleanup not held after the first boot")
			}

			// The empty state is written and loads normally from here on, so only the set-aside
			// file still shows that work was lost.
			if err := appState.UpdateJobData("j_new", models.JobData{JobID: "j_new"}); err != nil {
				t.Fatal(err)
			}
			restarted, restartedID, err := RestoreAppState(statePath)
			if err != nil {
				t.Fatalf("restore after restart: %v", err)
			}
			if restartedID != agentID {
				t.Fatalf("agent after restart = %q, want %q", restartedID, agentID)
			}
			if _, ok := restarted.GetJobData("j_new"); !ok {
				t.Fatal("job written after the first boot was not restored")
			}
			if restarted.CleanupHold() == "" {
				t.Fatal("cleanup not held after a restart")
			}
			if got := setAsideFiles(t, statePath); len(got) != 1 {
				t.Fatalf("set-aside files after restart = %v", got)
			}

			if err := os.Remove(asides[0]); err != nil {
				t.Fatal(err)
			}
			if hold := restarted.CleanupHold(); hold != "" {
				t.Fatalf("still held once cleared: %s", hold)
			}
		})
	}
}

func TestRestoreAppState_ReadFailureBoots(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := os.Mkdir(statePath, 0o755); err != nil {
		t.Fatal(err)
	}
	appState, _, err := RestoreAppState(statePath)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if appState.CleanupHold() == "" {
		t.Fatal("cleanup not held")
	}
}

func TestRestoreAppState_ExitsWithoutTouchingTheFileWhenRenameFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	content := []byte("not json")
	if err := os.WriteFile(statePath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	appState, _, err := RestoreAppState(statePath)
	if err == nil || appState != nil {
		t.Fatalf("restore = %v, %v, want an error and no state", appState, err)
	}
	if kept, readErr := os.ReadFile(statePath); readErr != nil || !bytes.Equal(kept, content) {
		t.Fatalf("state file = %q, %v, want it untouched", kept, readErr)
	}
}

func TestRestoreAppState_ReadableStateIsNotHeld(t *testing.T) {
	jobs := map[string]models.JobData{"j1": {JobID: "j1"}}
	cases := map[string]any{
		"normal":              map[string]any{"agent_id": "agent", "jobs": jobs},
		"no jobs":             map[string]any{"agent_id": "agent"},
		"jobs but no agentID": map[string]any{"jobs": jobs},
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "state.json")
			raw, err := json.Marshal(content)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(statePath, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			appState, _, err := RestoreAppState(statePath)
			if err != nil {
				t.Fatalf("restore: %v", err)
			}
			if hold := appState.CleanupHold(); hold != "" {
				t.Fatalf("held: %s", hold)
			}
			if got := setAsideFiles(t, statePath); len(got) != 0 {
				t.Fatalf("set-aside files = %v", got)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		appState, agentID, err := RestoreAppState(filepath.Join(t.TempDir(), "state.json"))
		if err != nil || agentID == "" {
			t.Fatalf("restore = %q, %v", agentID, err)
		}
		if hold := appState.CleanupHold(); hold != "" {
			t.Fatalf("held: %s", hold)
		}
	})
}
