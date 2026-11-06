package usecases

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"nairid/clients"
	"nairid/models"
)

type cleanupHoldFixture struct {
	gitUseCase *GitUseCase
	gitClient  *clients.GitClient
	mainRepo   string
	base       string
	statePath  string
}

func newCleanupHoldFixture(t *testing.T) cleanupHoldFixture {
	t.Helper()
	mainRepo, worktreeBase, cleanup := setupTestGitRepoWithRemote(t)
	t.Cleanup(cleanup)

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })

	statePath := filepath.Join(t.TempDir(), "state.json")
	appState := models.NewAppState("agent", statePath)
	appState.SetRepositoryContext(&models.RepositoryContext{RepoPath: mainRepo, IsRepoMode: true})
	gitUseCase := NewGitUseCase(gitClient, nil, appState)

	t.Setenv("AGENT_EXEC_USER", "")
	t.Setenv("HOME", worktreeBase)
	base, err := gitUseCase.GetWorktreeBasePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	gitUseCase.SetWorktreePool(NewWorktreePool(gitClient, base, 1))

	return cleanupHoldFixture{gitUseCase, gitClient, mainRepo, base, statePath}
}

func (f cleanupHoldFixture) setAsideState(t *testing.T) string {
	t.Helper()
	if err := os.WriteFile(f.statePath, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	asidePath, err := models.SetAsideUnreadableState(f.statePath)
	if err != nil {
		t.Fatal(err)
	}
	return asidePath
}

// addJobWorktree creates a worktree holding a file git doesn't know about, like work in progress.
func (f cleanupHoldFixture) addJobWorktree(t *testing.T, jobID, branch string) (string, string) {
	t.Helper()
	path := filepath.Join(f.base, jobID)
	if err := f.gitClient.CreateWorktree(path, branch, "origin/main"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "uncommitted.txt")
	if err := os.WriteFile(file, []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, file
}

func (f cleanupHoldFixture) branchExists(t *testing.T, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = f.mainRepo
	return cmd.Run() == nil
}

func (f cleanupHoldFixture) runAllCleanup(t *testing.T) {
	t.Helper()
	if err := f.gitUseCase.CleanupStaleJobWorktrees(); err != nil {
		t.Fatal(err)
	}
	if err := f.gitUseCase.CleanupOrphanedWorktrees(); err != nil {
		t.Fatal(err)
	}
	if err := f.gitUseCase.CleanupStaleBranches(); err != nil {
		t.Fatal(err)
	}
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestCleanup_DeletesNothingWhileHeldAndResumesOnceCleared(t *testing.T) {
	f := newCleanupHoldFixture(t)
	jobPath, jobFile := f.addJobWorktree(t, "job_lost", "nairid/lost")

	// A job worktree whose git link broke: only CleanupStaleJobWorktrees would remove this one.
	brokenPath := filepath.Join(f.base, "job_broken")
	if err := os.MkdirAll(brokenPath, 0o755); err != nil {
		t.Fatal(err)
	}
	brokenLink := []byte("gitdir: /nonexistent/.git/worktrees/job_broken")
	if err := os.WriteFile(filepath.Join(brokenPath, ".git"), brokenLink, 0o644); err != nil {
		t.Fatal(err)
	}

	unused := exec.Command("git", "branch", "nairid/unused", "origin/main")
	unused.Dir = f.mainRepo
	if out, err := unused.CombinedOutput(); err != nil {
		t.Fatalf("create branch: %v: %s", err, out)
	}

	asidePath := f.setAsideState(t)
	f.runAllCleanup(t)
	for _, path := range []string{jobFile, brokenPath} {
		if !pathExists(path) {
			t.Errorf("%s was deleted while held", path)
		}
	}
	for _, branch := range []string{"nairid/lost", "nairid/unused"} {
		if !f.branchExists(t, branch) {
			t.Errorf("branch %s was deleted while held", branch)
		}
	}

	if err := os.Remove(asidePath); err != nil {
		t.Fatal(err)
	}
	f.runAllCleanup(t)
	for _, path := range []string{jobPath, brokenPath} {
		if pathExists(path) {
			t.Errorf("%s survived cleanup after the hold was cleared", path)
		}
	}
	for _, branch := range []string{"nairid/lost", "nairid/unused"} {
		if f.branchExists(t, branch) {
			t.Errorf("branch %s survived cleanup after the hold was cleared", branch)
		}
	}
}

func TestPrepareWorktree_KeepsTheWorktreeOfAForgottenJobWhileHeld(t *testing.T) {
	f := newCleanupHoldFixture(t)
	jobPath, _ := f.addJobWorktree(t, "job_lost", "nairid/lost")
	f.setAsideState(t)

	branch, path, err := f.gitUseCase.PrepareForNewConversationWithWorktree("job_lost", "follow-up")
	if err != nil {
		t.Fatal(err)
	}
	if path != jobPath || branch == "nairid/lost" {
		t.Fatalf("got branch %s at %s, want a new branch at %s", branch, path, jobPath)
	}
	if pathExists(filepath.Join(path, "uncommitted.txt")) {
		t.Error("the new worktree inherited the old job's files")
	}
	if !f.gitClient.WorktreeExists(path) {
		t.Error("the new worktree is not registered")
	}

	kept, err := filepath.Glob(jobPath + ".kept-*")
	if err != nil || len(kept) != 1 {
		t.Fatalf("kept worktrees = %v, %v", kept, err)
	}
	if content, err := os.ReadFile(filepath.Join(kept[0], "uncommitted.txt")); err != nil || string(content) != "work" {
		t.Errorf("kept file = %q, %v", content, err)
	}
	if !f.branchExists(t, "nairid/lost") {
		t.Error("the old job's branch was deleted")
	}
}

// Git can't move this one, and leaving it in place would fail the job when the new worktree is added.
func TestPrepareWorktree_KeepsADirectoryGitNoLongerKnowsWhileHeld(t *testing.T) {
	f := newCleanupHoldFixture(t)
	jobPath, _ := f.addJobWorktree(t, "job_lost", "nairid/lost")
	if err := os.RemoveAll(filepath.Join(f.mainRepo, ".git", "worktrees", "job_lost")); err != nil {
		t.Fatal(err)
	}
	if f.gitClient.WorktreeExists(jobPath) {
		t.Fatal("git still lists the worktree")
	}
	f.setAsideState(t)

	_, path, err := f.gitUseCase.PrepareForNewConversationWithWorktree("job_lost", "follow-up")
	if err != nil {
		t.Fatal(err)
	}
	if !f.gitClient.WorktreeExists(path) {
		t.Error("the new worktree is not registered")
	}
	kept, err := filepath.Glob(jobPath + ".kept-*")
	if err != nil || len(kept) != 1 {
		t.Fatalf("kept worktrees = %v, %v", kept, err)
	}
	if !pathExists(filepath.Join(kept[0], "uncommitted.txt")) {
		t.Error("the kept directory lost its file")
	}
}

// Git still lists this one but refuses to move it, and refuses to add a worktree at a listed path.
func TestPrepareWorktree_KeepsAWorktreeGitCantMoveWhileHeld(t *testing.T) {
	f := newCleanupHoldFixture(t)
	jobPath, _ := f.addJobWorktree(t, "job_lost", "nairid/lost")
	if err := os.WriteFile(filepath.Join(jobPath, ".git"), []byte("gitdir: /nonexistent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !f.gitClient.WorktreeExists(jobPath) {
		t.Fatal("git no longer lists the worktree")
	}
	f.setAsideState(t)

	_, path, err := f.gitUseCase.PrepareForNewConversationWithWorktree("job_lost", "follow-up")
	if err != nil {
		t.Fatal(err)
	}
	if !f.gitClient.WorktreeExists(path) {
		t.Error("the new worktree is not registered")
	}
	kept, err := filepath.Glob(jobPath + ".kept-*")
	if err != nil || len(kept) != 1 {
		t.Fatalf("kept worktrees = %v, %v", kept, err)
	}
	if !pathExists(filepath.Join(kept[0], "uncommitted.txt")) {
		t.Error("the kept directory lost its file")
	}
}

// The operator is told clearing the hold deletes these too, so that has to stay true.
func TestCleanup_DeletesKeptWorktreesOnceCleared(t *testing.T) {
	f := newCleanupHoldFixture(t)
	jobPath, _ := f.addJobWorktree(t, "job_lost", "nairid/lost")
	asidePath := f.setAsideState(t)
	if _, _, err := f.gitUseCase.PrepareForNewConversationWithWorktree("job_lost", "follow-up"); err != nil {
		t.Fatal(err)
	}
	kept, err := filepath.Glob(jobPath + ".kept-*")
	if err != nil || len(kept) != 1 {
		t.Fatalf("kept worktrees = %v, %v", kept, err)
	}

	f.runAllCleanup(t)
	if !pathExists(kept[0]) {
		t.Fatal("kept worktree deleted while held")
	}
	if err := os.Remove(asidePath); err != nil {
		t.Fatal(err)
	}
	f.runAllCleanup(t)
	if pathExists(kept[0]) {
		t.Error("kept worktree survived cleanup after the hold was cleared")
	}
}

func TestPrepareWorktree_StillReplacesAnExistingWorktreeWhenNotHeld(t *testing.T) {
	f := newCleanupHoldFixture(t)
	jobPath, _ := f.addJobWorktree(t, "job_retry", "nairid/retry")

	_, path, err := f.gitUseCase.PrepareForNewConversationWithWorktree("job_retry", "retry")
	if err != nil {
		t.Fatal(err)
	}
	if path != jobPath || pathExists(filepath.Join(path, "uncommitted.txt")) {
		t.Errorf("got %s with the old files still in it", path)
	}
	if kept, _ := filepath.Glob(jobPath + ".kept-*"); len(kept) != 0 {
		t.Errorf("kept worktrees = %v, want none", kept)
	}
}
