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

// isolatedGit runs git with no user or system config, so signing or hooks on the
// machine running the tests cannot change the result.
func isolatedGit(t *testing.T) func(dir string, args ...string) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, name := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(name, "test")
	}
	for _, name := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(name, "test@example.com")
	}
	return func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
}

func repoModeGitUseCase(t *testing.T) *GitUseCase {
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	appState.SetRepositoryContext(&models.RepositoryContext{IsRepoMode: true})
	return NewGitUseCase(clients.NewGitClient(), nil, appState)
}

func TestCommitStoppedWork_KeepsUnpushedWorkAsALocalWIPCommit(t *testing.T) {
	git := isolatedGit(t)
	repo := t.TempDir()
	git(repo, "init", "-q", "-b", "agent-branch")
	git(repo, "commit", "-q", "--allow-empty", "-m", "base")
	if err := os.WriteFile(filepath.Join(repo, "half-done.txt"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	useCase := repoModeGitUseCase(t)

	if err := useCase.CommitStoppedWork(repo, "agent-branch", "Lucy"); err != nil {
		t.Fatalf("CommitStoppedWork: %v", err)
	}

	if status := git(repo, "status", "--porcelain"); status != "" {
		t.Errorf("tree still dirty after the stop commit:\n%s", status)
	}
	if subject := git(repo, "log", "-1", "--format=%s"); subject != "WIP: turn stopped by Lucy" {
		t.Errorf("commit subject = %q", subject)
	}

	// Nothing to commit is not an error and adds no commit.
	if err := useCase.CommitStoppedWork(repo, "agent-branch", "Lucy"); err != nil {
		t.Fatalf("second CommitStoppedWork: %v", err)
	}
	if count := git(repo, "rev-list", "--count", "HEAD"); count != "2" {
		t.Errorf("commits = %s, want 2", count)
	}
}

func TestCommitStoppedWork_PushesABranchAlreadyOnTheRemote(t *testing.T) {
	git := isolatedGit(t)
	remote := t.TempDir()
	git(remote, "init", "-q", "--bare", "-b", "agent-branch")
	repo := t.TempDir()
	git(repo, "init", "-q", "-b", "agent-branch")
	git(repo, "commit", "-q", "--allow-empty", "-m", "base")
	git(repo, "remote", "add", "origin", remote)
	git(repo, "push", "-q", "-u", "origin", "agent-branch")
	if err := os.WriteFile(filepath.Join(repo, "half-done.txt"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := repoModeGitUseCase(t).CommitStoppedWork(repo, "agent-branch", "Lucy"); err != nil {
		t.Fatalf("CommitStoppedWork: %v", err)
	}

	if local, onRemote := git(repo, "rev-parse", "HEAD"), git(remote, "rev-parse", "agent-branch"); local != onRemote {
		t.Errorf("remote is at %s, want the WIP commit %s", onRemote, local)
	}
}

func TestCommitStoppedWork_DoesNothingOutsideRepoMode(t *testing.T) {
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	appState.SetRepositoryContext(&models.RepositoryContext{IsRepoMode: false})
	useCase := NewGitUseCase(nil, nil, appState)
	if err := useCase.CommitStoppedWork(t.TempDir(), "agent-branch", "Lucy"); err != nil {
		t.Fatalf("CommitStoppedWork outside repo mode: %v", err)
	}
}
