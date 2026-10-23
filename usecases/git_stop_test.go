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

// stoppedJobRepo sets up a clone of a bare remote the way nairid prepares one, with the job's
// branch cut from origin/main, and a half-done file left in the job's working tree.
type stoppedJobRepo struct {
	git      func(dir string, args ...string) string
	remote   string
	repo     string
	worktree string
	useCase  *GitUseCase
}

func newStoppedJobRepo(t *testing.T) *stoppedJobRepo {
	git := isolatedGit(t)
	r := &stoppedJobRepo{git: git, remote: t.TempDir(), repo: t.TempDir()}
	git(r.remote, "init", "-q", "--bare", "-b", "main")
	git(r.repo, "init", "-q", "-b", "main")
	git(r.repo, "commit", "-q", "--allow-empty", "-m", "base")
	git(r.repo, "remote", "add", "origin", r.remote)
	git(r.repo, "push", "-q", "-u", "origin", "main")

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return r.repo })
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	appState.SetRepositoryContext(&models.RepositoryContext{IsRepoMode: true})
	r.useCase = NewGitUseCase(gitClient, nil, appState)

	r.worktree = filepath.Join(t.TempDir(), "job")
	if err := gitClient.CreateWorktree(r.worktree, "agent-branch", "origin/main"); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	writeHalfDoneFile(t, r.worktree)
	return r
}

func writeHalfDoneFile(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "half-done.txt"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *stoppedJobRepo) onRemote(branch string) string {
	return r.git(r.repo, "ls-remote", "--heads", "origin", branch)
}

func TestCommitStoppedWork_KeepsAFirstTurnsWorkLocal(t *testing.T) {
	r := newStoppedJobRepo(t)

	if err := r.useCase.CommitStoppedWork(r.worktree); err != nil {
		t.Fatalf("CommitStoppedWork: %v", err)
	}

	if status := r.git(r.worktree, "status", "--porcelain"); status != "" {
		t.Errorf("tree still dirty after the stop commit:\n%s", status)
	}
	if subject := r.git(r.worktree, "log", "-1", "--format=%s"); subject != "WIP: turn stopped" {
		t.Errorf("commit subject = %q", subject)
	}
	if onRemote := r.onRemote("agent-branch"); onRemote != "" {
		t.Errorf("a branch that was never pushed must stay local, remote has %q", onRemote)
	}

	// Nothing to commit is not an error and adds no commit.
	if err := r.useCase.CommitStoppedWork(r.worktree); err != nil {
		t.Fatalf("second CommitStoppedWork: %v", err)
	}
	if count := r.git(r.worktree, "rev-list", "--count", "HEAD"); count != "2" {
		t.Errorf("commits = %s, want 2", count)
	}
}

func TestCommitStoppedWork_PushesABranchAlreadyOnTheRemote(t *testing.T) {
	r := newStoppedJobRepo(t)
	r.git(r.worktree, "push", "-q", "-u", "origin", "agent-branch")

	if err := r.useCase.CommitStoppedWork(r.worktree); err != nil {
		t.Fatalf("CommitStoppedWork: %v", err)
	}

	if local, onRemote := r.git(r.worktree, "rev-parse", "HEAD"), r.git(r.remote, "rev-parse", "agent-branch"); local != onRemote {
		t.Errorf("remote is at %s, want the WIP commit %s", onRemote, local)
	}
}

func TestCommitStoppedWork_InTheRepositoryItself(t *testing.T) {
	r := newStoppedJobRepo(t)
	r.git(r.repo, "checkout", "-q", "-b", "repo-branch", "origin/main")
	writeHalfDoneFile(t, r.repo)

	if err := r.useCase.CommitStoppedWork(""); err != nil {
		t.Fatalf("CommitStoppedWork: %v", err)
	}

	if subject := r.git(r.repo, "log", "-1", "--format=%s"); subject != "WIP: turn stopped" {
		t.Errorf("commit subject = %q", subject)
	}
	if onRemote := r.onRemote("repo-branch"); onRemote != "" {
		t.Errorf("a branch that was never pushed must stay local, remote has %q", onRemote)
	}
}

func TestCommitStoppedWork_DoesNothingOutsideRepoMode(t *testing.T) {
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	appState.SetRepositoryContext(&models.RepositoryContext{IsRepoMode: false})
	useCase := NewGitUseCase(nil, nil, appState)
	if err := useCase.CommitStoppedWork(t.TempDir()); err != nil {
		t.Fatalf("CommitStoppedWork outside repo mode: %v", err)
	}
}
