package models

import (
	"path/filepath"
	"testing"
)

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
