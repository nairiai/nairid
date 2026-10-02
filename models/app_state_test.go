package models

import (
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
		ReasoningEffort:    "xhigh",
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
