package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// A follow-up message reads the job back before it runs the CLI again, so a field
// the read drops (the reasoning effort was one) is lost from the second turn on.
func TestJobDataIsReadBackWithEveryField(t *testing.T) {
	want := JobData{
		JobID:              "j1",
		BranchName:         "nairi/fix-login",
		WorktreePath:       "/tmp/worktrees/j1",
		ClaudeSessionID:    "ses_1",
		PullRequestID:      "42",
		LastMessage:        "fix the login test",
		ProcessedMessageID: "cmsg_1",
		MessageLink:        "https://example.slack.com/archives/C1/p1",
		Status:             JobStatusCompleted,
		Mode:               AgentModeExecute,
		SystemPrompt:       "Org rule: never push to main.",
		ReasoningEffort:    "xhigh",
		EffortModel:        "claude-opus-4-8",
		SessionTimeoutMs:   7200000,
		StartPayload:       json.RawMessage(`{"job_id":"j1"}`),
		UpdatedAt:          time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC),
	}
	fields := reflect.ValueOf(want)
	for i := 0; i < fields.NumField(); i++ {
		if fields.Field(i).IsZero() {
			t.Fatalf("set JobData.%s in this test, so that reading it back is checked", fields.Type().Field(i).Name)
		}
	}

	statePath := filepath.Join(t.TempDir(), "state.json")
	state := NewAppState("agent", statePath)
	if err := state.UpdateJobData("j1", want); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	got, exists := state.GetJobData("j1")
	if !exists || !reflect.DeepEqual(*got, want) {
		t.Errorf("GetJobData = %+v, want %+v", got, want)
	}
	if all := state.GetAllJobs(); !reflect.DeepEqual(all["j1"], want) {
		t.Errorf("GetAllJobs = %+v, want %+v", all["j1"], want)
	}
}

func TestSetJobPullRequestIDKeepsConcurrentStatusChange(t *testing.T) {
	state := NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	if err := state.UpdateJobData("j1", JobData{JobID: "j1", Status: JobStatusInProgress}); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	// Idle check takes its snapshot while the job is still running.
	snapshot, _ := state.GetJobData("j1")

	// The job handler finishes in between.
	completed := *snapshot
	completed.Status = JobStatusCompleted
	if err := state.UpdateJobData("j1", completed); err != nil {
		t.Fatalf("complete job: %v", err)
	}

	exists, err := state.SetJobPullRequestID("j1", "none")
	if err != nil || !exists {
		t.Fatalf("SetJobPullRequestID: exists=%v err=%v", exists, err)
	}

	got, _ := state.GetJobData("j1")
	if got.Status != JobStatusCompleted {
		t.Errorf("status = %q, want %q", got.Status, JobStatusCompleted)
	}
	if got.PullRequestID != "none" {
		t.Errorf("PullRequestID = %q, want %q", got.PullRequestID, "none")
	}
}

func TestSetJobPullRequestIDDoesNotRecreateRemovedJob(t *testing.T) {
	state := NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	if err := state.UpdateJobData("j1", JobData{JobID: "j1"}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if err := state.RemoveJob("j1"); err != nil {
		t.Fatalf("remove job: %v", err)
	}

	exists, err := state.SetJobPullRequestID("j1", "none")
	if err != nil || exists {
		t.Fatalf("SetJobPullRequestID: exists=%v err=%v", exists, err)
	}
	if _, ok := state.GetJobData("j1"); ok {
		t.Error("removed job was recreated")
	}
}

// A crash after any write during a restore must leave a file that still holds every part.
func TestRestore_WritesTheWholeSnapshotOnce(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	now := time.Now()
	var writes []*LoadedState
	afterPersist = func(path string) {
		loaded, err := LoadState(path)
		if err != nil {
			t.Errorf("load after write: %v", err)
			return
		}
		writes = append(writes, loaded)
	}
	t.Cleanup(func() { afterPersist = nil })

	state := NewAppState("agent", statePath)
	err := state.Restore(
		map[string]*JobData{"j1": {JobID: "j1", Status: JobStatusCompleted}, "j2": {JobID: "j2", Status: JobStatusCompleted}},
		map[string]*QueuedMessage{"cmsg_q": {ProcessedMessageID: "cmsg_q", JobID: "j1", QueuedAt: now}},
		map[string]time.Time{"cmsg_done": now},
		map[string]*OutboxMessage{"msg_1": {ID: "msg_1", Event: "cc_message", Data: []byte(`{}`), QueuedAt: now}},
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(writes) != 1 {
		t.Fatalf("restore wrote the state %d times, want once", len(writes))
	}
	got := writes[0]
	if len(got.Jobs) != 2 || len(got.QueuedMessages) != 1 || len(got.DoneMessages) != 1 || len(got.Outbox) != 1 {
		t.Errorf("written state is missing parts: %d jobs, %d queued, %d done, %d unsent", len(got.Jobs), len(got.QueuedMessages), len(got.DoneMessages), len(got.Outbox))
	}
}

func TestCleanupHold_FollowsTheSetAsideFile(t *testing.T) {
	// Glob characters in the path must not hide the file.
	dir := filepath.Join(t.TempDir(), "in[stance]*")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, "state.json")
	bad := []byte("not json")
	if err := os.WriteFile(statePath, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	state := NewAppState("agent", statePath)
	if hold := state.CleanupHold(); hold != "" {
		t.Fatalf("held before anything was set aside: %s", hold)
	}

	asidePath, err := SetAsideUnreadableState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if kept, err := os.ReadFile(asidePath); err != nil || string(kept) != string(bad) {
		t.Fatalf("set-aside file = %q, %v", kept, err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state file still in place: %v", err)
	}
	if state.CleanupHold() == "" {
		t.Fatal("not held after the file was set aside")
	}

	// A valid state written later doesn't lift it, whichever process reads it.
	if err := state.UpdateJobData("j1", JobData{JobID: "j1"}); err != nil {
		t.Fatal(err)
	}
	if NewAppState("agent", statePath).CleanupHold() == "" {
		t.Fatal("not held after a restart")
	}

	if err := os.Remove(asidePath); err != nil {
		t.Fatal(err)
	}
	if hold := state.CleanupHold(); hold != "" {
		t.Fatalf("still held after the file was removed: %s", hold)
	}
}

func TestCleanupHold_NeedsEverySetAsideFileRemoved(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	var asidePaths []string
	for range 2 {
		if err := os.WriteFile(statePath, []byte("not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		asidePath, err := SetAsideUnreadableState(statePath)
		if err != nil {
			t.Fatal(err)
		}
		asidePaths = append(asidePaths, asidePath)
	}
	if asidePaths[0] == asidePaths[1] {
		t.Fatal("second set-aside overwrote the first")
	}
	if err := os.Remove(asidePaths[0]); err != nil {
		t.Fatal(err)
	}
	if NewAppState("agent", statePath).CleanupHold() == "" {
		t.Fatal("not held with one set-aside file left")
	}
}

func TestCleanupHold_FailsClosedWhenTheDirectoryCantBeListed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if NewAppState("agent", filepath.Join(dir, "state.json")).CleanupHold() == "" {
		t.Fatal("not held when the directory can't be listed")
	}
}

func TestCleanupHold_IgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"state.json", "state.json.tmp", "other.json.unreadable-1"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if hold := NewAppState("agent", filepath.Join(dir, "state.json")).CleanupHold(); hold != "" {
		t.Fatalf("held by an unrelated file: %s", hold)
	}
	missing := filepath.Join(dir, "no-such-dir", "state.json")
	if hold := NewAppState("agent", missing).CleanupHold(); hold != "" {
		t.Fatalf("held by a missing directory: %s", hold)
	}
}

// One write is what makes a crash leave either all of a finished turn or none of it.
func TestCompleteTurnIsOneWrite(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	appState := NewAppState("agent", statePath)
	if err := appState.AddQueuedMessage(QueuedMessage{ProcessedMessageID: "cmsg_1", JobID: "j1", QueuedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	writes := 0
	restore := SetAfterPersistForTest(func(path string) {
		if path == statePath {
			writes++
		}
	})
	defer restore()

	err := appState.CompleteTurn(
		JobData{JobID: "j1", ProcessedMessageID: "cmsg_1", Status: JobStatusCompleted, ClaudeSessionID: "session-1"},
		&OutboxMessage{ID: "out_1", Event: "cc_message", Data: json.RawMessage(`{}`), QueuedAt: time.Now()},
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Errorf("finishing a turn took %d writes, want 1", writes)
	}

	loaded, err := LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if job := loaded.Jobs["j1"]; job == nil || job.Status != JobStatusCompleted || job.ClaudeSessionID != "session-1" {
		t.Errorf("job read back as %+v", job)
	}
	if loaded.Outbox["out_1"] == nil {
		t.Error("reply was not stored")
	}
	if _, done := loaded.DoneMessages["cmsg_1"]; !done {
		t.Error("message was not marked done")
	}
	if loaded.QueuedMessages["cmsg_1"] != nil {
		t.Error("message is still queued")
	}
}

func TestCompleteTurnWithNoOutboxEntryStillFinishesTheTurn(t *testing.T) {
	appState := NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	err := appState.CompleteTurn(JobData{JobID: "j1", ProcessedMessageID: "cmsg_1", Status: JobStatusCompleted}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !appState.IsMessageDone("cmsg_1") {
		t.Error("message was not marked done")
	}
	if len(appState.GetAllOutboxMessages()) != 0 {
		t.Error("nothing should be in the outbox")
	}
}
