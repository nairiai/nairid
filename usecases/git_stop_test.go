package usecases

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nairid/clients"
	"nairid/models"
)

// A stopped turn's work is kept as a plain local commit: no Claude call, no push, no PR.
func TestCommitStoppedWork_KeepsTheWorkAsALocalWIPCommit(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "base")
	if err := os.WriteFile(filepath.Join(repo, "half-done.txt"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	appState.SetRepositoryContext(&models.RepositoryContext{IsRepoMode: true})
	useCase := NewGitUseCase(clients.NewGitClient(), nil, appState)

	if err := useCase.CommitStoppedWork(repo, "Lucy"); err != nil {
		t.Fatalf("CommitStoppedWork: %v", err)
	}

	if status := git("status", "--porcelain"); status != "" {
		t.Errorf("tree still dirty after the stop commit:\n%s", status)
	}
	if subject := git("log", "-1", "--format=%s"); subject != "WIP: turn stopped by Lucy" {
		t.Errorf("commit subject = %q", subject)
	}
	if count := git("rev-list", "--count", "HEAD"); count != "2" {
		t.Errorf("commits = %s, want 2", count)
	}

	// Nothing to commit is not an error and adds no commit.
	if err := useCase.CommitStoppedWork(repo, "Lucy"); err != nil {
		t.Fatalf("second CommitStoppedWork: %v", err)
	}
	if count := git("rev-list", "--count", "HEAD"); count != "2" {
		t.Errorf("commits after a clean stop = %s, want 2", count)
	}
}

func TestCommitStoppedWork_DoesNothingOutsideRepoMode(t *testing.T) {
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	appState.SetRepositoryContext(&models.RepositoryContext{IsRepoMode: false})
	useCase := NewGitUseCase(nil, nil, appState)
	if err := useCase.CommitStoppedWork(t.TempDir(), "Lucy"); err != nil {
		t.Fatalf("CommitStoppedWork outside repo mode: %v", err)
	}
}
