package usecases

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"nairid/clients"
	"nairid/models"
	"nairid/services"
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
	appState.SetRepositoryContext(&models.RepositoryContext{IsRepoMode: true, RepoPath: r.repo})
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

func (r *stoppedJobRepo) remoteHead(branch string) string {
	return r.git(r.repo, "ls-remote", "--heads", "origin", branch)
}

func TestCommitStoppedWork_PushesAFirstTurnToTheJobsBranch(t *testing.T) {
	r := newStoppedJobRepo(t)

	if err := r.useCase.CommitStoppedWork(r.worktree, "agent-branch"); err != nil {
		t.Fatalf("CommitStoppedWork: %v", err)
	}

	if status := r.git(r.worktree, "status", "--porcelain"); status != "" {
		t.Errorf("tree still dirty after the stop commit:\n%s", status)
	}
	if subject := r.git(r.worktree, "log", "-1", "--format=%s"); subject != "WIP: turn stopped" {
		t.Errorf("commit subject = %q", subject)
	}
	if local, remote := r.git(r.worktree, "rev-parse", "HEAD"), r.git(r.remote, "rev-parse", "agent-branch"); local != remote {
		t.Errorf("remote agent-branch is at %s, want the WIP commit %s", remote, local)
	}
	if upstream := r.git(r.worktree, "rev-parse", "--abbrev-ref", "@{upstream}"); upstream != "origin/agent-branch" {
		t.Errorf("upstream = %q, want origin/agent-branch so the next pull does not replay the WIP onto main", upstream)
	}

	// Nothing to commit is not an error and adds no commit.
	if err := r.useCase.CommitStoppedWork(r.worktree, "agent-branch"); err != nil {
		t.Fatalf("second CommitStoppedWork: %v", err)
	}
	if count := r.git(r.worktree, "rev-list", "--count", "HEAD"); count != "2" {
		t.Errorf("commits = %s, want 2", count)
	}
}

func TestCommitStoppedWork_InTheRepositoryItself(t *testing.T) {
	r := newStoppedJobRepo(t)
	r.git(r.repo, "checkout", "-q", "-b", "repo-branch", "origin/main")
	writeHalfDoneFile(t, r.repo)

	if err := r.useCase.CommitStoppedWork("", "repo-branch"); err != nil {
		t.Fatalf("CommitStoppedWork: %v", err)
	}

	if subject := r.git(r.remote, "log", "-1", "--format=%s", "repo-branch"); subject != "WIP: turn stopped" {
		t.Errorf("remote repo-branch head = %q, want the WIP commit", subject)
	}
}

func TestCommitStoppedWork_LeavesAnotherCheckedOutBranchAlone(t *testing.T) {
	r := newStoppedJobRepo(t)
	mainBefore := r.remoteHead("main")
	writeHalfDoneFile(t, r.repo)

	err := r.useCase.CommitStoppedWork("", "repo-branch")
	if err == nil || !strings.Contains(err.Error(), `"main" checked out instead of the job's branch "repo-branch"`) {
		t.Fatalf("got %v, want an error naming both branches", err)
	}
	if mainAfter := r.remoteHead("main"); mainAfter != mainBefore {
		t.Errorf("origin/main moved from %q to %q", mainBefore, mainAfter)
	}
	if subject := r.git(r.repo, "log", "-1", "--format=%s"); subject != "base" {
		t.Errorf("main got a commit %q", subject)
	}
}

func TestCommitStoppedWork_ReportsACommitAHookRejected(t *testing.T) {
	r := newStoppedJobRepo(t)
	hook := filepath.Join(r.repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := r.useCase.CommitStoppedWork(r.worktree, "agent-branch"); err == nil {
		t.Fatal("want the hook's rejection reported")
	}
	if onRemote := r.remoteHead("agent-branch"); onRemote != "" {
		t.Errorf("nothing may be pushed after a rejected commit, remote has %q", onRemote)
	}
}

func TestCommitStoppedWork_DoesNothingOutsideRepoMode(t *testing.T) {
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	appState.SetRepositoryContext(&models.RepositoryContext{IsRepoMode: false})
	useCase := NewGitUseCase(nil, nil, appState)
	if err := useCase.CommitStoppedWork(t.TempDir(), "agent-branch"); err != nil {
		t.Fatalf("CommitStoppedWork outside repo mode: %v", err)
	}
}

type commitMessageAgent struct{ services.CLIAgent }

func (commitMessageAgent) ContinueConversationInDir(_, _, _ string) (*services.CLIAgentResult, error) {
	return &services.CLIAgentResult{Output: "Add the half-done file"}, nil
}

func TestAutoCommitInWorktree_AJobGivenToAnotherAgentIsNotPushed(t *testing.T) {
	r := newStoppedJobRepo(t)
	r.useCase.claudeService = commitMessageAgent{}

	_, err := r.useCase.AutoCommitChangesInWorktreeIfNeeded("", "session", r.worktree, func() bool { return true })

	if !errors.Is(err, ErrJobReleased) {
		t.Fatalf("want ErrJobReleased, got %v", err)
	}
	if head := r.remoteHead("agent-branch"); head != "" {
		t.Fatalf("the branch reached the remote: %s", head)
	}
}

// fakeGH stands in for the GitHub CLI: the branch has no PR yet, and each PR it is asked to open is recorded.
func fakeGH(t *testing.T) (openedPRs func() string) {
	t.Helper()
	bin := t.TempDir()
	opened := filepath.Join(bin, "opened")
	script := "#!/bin/sh\ncase \"$1 $2\" in\n" +
		"\"pr list\") echo '[]' ;;\n" +
		"\"pr create\") echo \"$@\" >> " + opened + "; echo https://github.com/acme/app/pull/42 ;;\n" +
		"*) exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string {
		content, _ := os.ReadFile(opened)
		return string(content)
	}
}

// switchedWhileWritingThePR is a CLI whose job goes to another agent while it writes the PR title.
type switchedWhileWritingThePR struct {
	services.CLIAgent
	switched *atomic.Bool
}

func (a switchedWhileWritingThePR) ContinueConversationInDir(_, prompt, _ string) (*services.CLIAgentResult, error) {
	if prompt == PRTitleGenerationPrompt("agent-branch") {
		a.switched.Store(true)
	}
	return &services.CLIAgentResult{Output: "Add the half-done file"}, nil
}

func TestAutoCommitInWorktree_AJobGivenToAnotherAgentWhileItsPRIsWrittenGetsNoPR(t *testing.T) {
	r := newStoppedJobRepo(t)
	openedPRs := fakeGH(t)
	switched := &atomic.Bool{}
	r.useCase.claudeService = switchedWhileWritingThePR{switched: switched}

	_, err := r.useCase.AutoCommitChangesInWorktreeIfNeeded("", "session", r.worktree, switched.Load)

	if !errors.Is(err, ErrJobReleased) {
		t.Fatalf("want ErrJobReleased, got %v", err)
	}
	if opened := openedPRs(); opened != "" {
		t.Errorf("a PR was opened for a job given to another agent: %s", opened)
	}
}

// A released stop in the shared checkout leaves it on the job's branch with tracked edits
// uncommitted. They used to block the checkout of main, so every new conversation failed.
func TestPrepareForNewConversation_StartsAfterATurnCutShortInTheSharedCheckout(t *testing.T) {
	git := isolatedGit(t)
	remote, repo := t.TempDir(), t.TempDir()
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(remote, "init", "-q", "--bare", "-b", "main")
	git(repo, "init", "-q", "-b", "main")
	write("base")
	git(repo, "add", "notes.txt")
	git(repo, "commit", "-q", "-m", "base")
	git(repo, "remote", "add", "origin", remote)
	git(repo, "push", "-q", "-u", "origin", "main")
	git(repo, "checkout", "-q", "-b", "agent-branch")
	write("first turn")
	git(repo, "commit", "-q", "-am", "first turn")
	write("half done")

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return repo })
	appState := models.NewAppState("agent", filepath.Join(t.TempDir(), "state.json"))
	appState.SetRepositoryContext(&models.RepositoryContext{IsRepoMode: true, RepoPath: repo})
	branch, err := NewGitUseCase(gitClient, nil, appState).PrepareForNewConversation("hint")
	if err != nil {
		t.Fatalf("PrepareForNewConversation: %v", err)
	}

	if current := git(repo, "branch", "--show-current"); current != branch {
		t.Errorf("current branch = %q, want the new conversation's %q", current, branch)
	}
	if status := git(repo, "status", "--porcelain"); status != "" {
		t.Errorf("the new conversation starts on a dirty checkout: %q", status)
	}
}
