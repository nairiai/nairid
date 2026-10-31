package usecases

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nairid/clients"
	"nairid/models"
)

// gitSpyScript logs every git invocation and, while the gate file exists, holds every
// pool worktree creation after the first one. It then execs the real git binary.
const gitSpyScript = `#!/bin/sh
printf '%s\n' "$*" >> "$GIT_SPY_LOG"
case "$*" in
"worktree add "*pool-ready*)
	if [ "$(grep -c '^worktree add .*pool-ready' "$GIT_SPY_LOG")" -gt 1 ]; then
		while [ -e "$GIT_SPY_GATE" ]; do sleep 0.05; done
	fi
	;;
esac
exec "$GIT_SPY_REAL" "$@"
`

type gitSpy struct {
	logPath  string
	gatePath string
}

// installGitSpy puts a logging git wrapper first on PATH for the rest of the test.
// If gated, pool worktree creations after the first block until openGate is called.
func installGitSpy(t *testing.T, gated bool) *gitSpy {
	t.Helper()

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git not found: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(gitSpyScript), 0755); err != nil {
		t.Fatalf("Failed to write git spy: %v", err)
	}

	spy := &gitSpy{logPath: filepath.Join(dir, "calls.log"), gatePath: filepath.Join(dir, "gate")}
	if gated {
		if err := os.WriteFile(spy.gatePath, nil, 0644); err != nil {
			t.Fatalf("Failed to create gate: %v", err)
		}
		t.Cleanup(spy.openGate)
	}

	t.Setenv("GIT_SPY_LOG", spy.logPath)
	t.Setenv("GIT_SPY_GATE", spy.gatePath)
	t.Setenv("GIT_SPY_REAL", realGit)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return spy
}

func (s *gitSpy) openGate() {
	_ = os.Remove(s.gatePath)
}

// count returns how many logged git invocations start with prefix.
func (s *gitSpy) count(t *testing.T, prefix string) int {
	t.Helper()
	data, err := os.ReadFile(s.logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("Failed to read git spy log: %v", err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(output))
	}
	return strings.TrimSpace(string(output))
}

func waitForPoolSize(t *testing.T, pool *WorktreePool, size int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for pool.GetPoolSize() < size {
		if time.Now().After(deadline) {
			t.Fatalf("Pool only filled to %d, expected %d", pool.GetPoolSize(), size)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForInitialFill(t *testing.T, pool *WorktreePool) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		pool.WaitForInitialFill()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("WaitForInitialFill did not return")
	}
}

func TestFillToTarget_FetchesAndResolvesDefaultBranchOncePerCycle(t *testing.T) {
	mainRepo, worktreeBase, cleanup := setupTestGitRepoWithRemote(t)
	defer cleanup()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })
	pool := NewWorktreePool(gitClient, worktreeBase, 3)

	spy := installGitSpy(t, false)
	pool.fillToTarget(context.Background())

	if pool.GetPoolSize() != 3 {
		t.Fatalf("Expected 3 pool worktrees, got %d", pool.GetPoolSize())
	}
	if n := spy.count(t, "fetch "); n != 1 {
		t.Errorf("Expected 1 fetch for the whole fill, got %d", n)
	}
	if n := spy.count(t, "remote show origin"); n != 1 {
		t.Errorf("Expected 1 default branch lookup for the whole fill, got %d", n)
	}
	if n := spy.count(t, "reset --hard"); n != 1 {
		t.Errorf("Expected 1 main repo reset for the whole fill, got %d", n)
	}
	if n := spy.count(t, "worktree add "); n != 3 {
		t.Errorf("Expected 3 worktree creations, got %d", n)
	}

	// Every worktree is based on the freshly fetched origin/main
	originCommit := gitOutput(t, mainRepo, "rev-parse", "origin/main")
	for _, wt := range pool.ready {
		if wt.BaseCommit != originCommit {
			t.Errorf("Worktree %s recorded base %s, expected %s", wt.Path, wt.BaseCommit, originCommit)
		}
		if head := gitOutput(t, wt.Path, "rev-parse", "HEAD"); head != originCommit {
			t.Errorf("Worktree %s is at %s, expected %s", wt.Path, head, originCommit)
		}
	}

	// A full pool does not touch the main repo or the remote at all
	pool.fillToTarget(context.Background())
	if n := spy.count(t, "fetch "); n != 1 {
		t.Errorf("Expected no extra fetch when the pool is full, got %d fetches", n)
	}
}

func TestWaitForInitialFill_ReleasedAfterFirstWorktree(t *testing.T) {
	mainRepo, worktreeBase, cleanup := setupTestGitRepoWithRemote(t)
	defer cleanup()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })
	pool := NewWorktreePool(gitClient, worktreeBase, 3)

	spy := installGitSpy(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()
	defer spy.openGate() // a gated git call would otherwise block Stop on failure

	// The second pool worktree is held by the gate, so startup must be released with one ready
	waitForInitialFill(t, pool)
	if size := pool.GetPoolSize(); size != 1 {
		t.Fatalf("Expected 1 ready worktree when the initial fill is released, got %d", size)
	}

	spy.openGate()
	waitForPoolSize(t, pool, 3)
}

func TestWaitForInitialFill_ReleasedWhenFillFails(t *testing.T) {
	notARepo := t.TempDir()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return notARepo })
	pool := NewWorktreePool(gitClient, filepath.Join(notARepo, "worktrees"), 3)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	defer pool.Stop()

	waitForInitialFill(t, pool)
	if size := pool.GetPoolSize(); size != 0 {
		t.Errorf("Expected an empty pool after a failed fill, got %d", size)
	}
}

// Jobs arriving while the rest of the pool fills in the background must not block, must not
// race the pool on the main repo, and must not have the main repo reset underneath them.
func TestJobsArrivingDuringFill(t *testing.T) {
	mainRepo, tempHome, cleanup := setupTestGitRepoWithRemote(t)
	defer cleanup()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })

	appState := models.NewAppState("test-agent", filepath.Join(tempHome, "state.json"))
	appState.SetRepositoryContext(&models.RepositoryContext{
		RepoPath:   mainRepo,
		IsRepoMode: true,
	})
	gitUseCase := NewGitUseCase(gitClient, nil, appState)
	t.Setenv("AGENT_EXEC_USER", "")
	t.Setenv("HOME", tempHome)
	worktreeBase, err := gitUseCase.GetWorktreeBasePath()
	if err != nil {
		t.Fatalf("Failed to get worktree base path: %v", err)
	}

	pool := NewWorktreePool(gitClient, worktreeBase, 3)
	gitUseCase.SetWorktreePool(pool)

	legacyBranch := "nairid/legacy-job"
	gitOutput(t, mainRepo, "branch", legacyBranch)

	spy := installGitSpy(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	stopPool := sync.OnceFunc(pool.Stop)
	defer stopPool()
	defer spy.openGate() // a gated git call would otherwise block Stop on failure
	waitForInitialFill(t, pool)

	// First job takes the ready worktree
	_, pathA, err := gitUseCase.PrepareForNewConversationWithWorktree("job_a", "first")
	if err != nil {
		t.Fatalf("Job A failed: %v", err)
	}
	if pathA != filepath.Join(worktreeBase, "job_a") {
		t.Errorf("Expected job A to get a pool worktree at %s, got %s", filepath.Join(worktreeBase, "job_a"), pathA)
	}

	// Second job finds the pool empty and falls back to synchronous creation without waiting
	_, pathB, err := gitUseCase.PrepareForNewConversationWithWorktree("job_b", "second")
	if err != nil {
		t.Fatalf("Job B failed: %v", err)
	}
	if !gitClient.WorktreeExists(pathB) {
		t.Errorf("Expected job B worktree at %s", pathB)
	}

	// A recovered branch-mode job switches the main repo to its branch and starts working
	if err := gitUseCase.SwitchToJobBranch(legacyBranch); err != nil {
		t.Fatalf("SwitchToJobBranch failed: %v", err)
	}
	workFile := filepath.Join(mainRepo, "in-progress.txt")
	if err := os.WriteFile(workFile, []byte("job work"), 0644); err != nil {
		t.Fatalf("Failed to write job work: %v", err)
	}
	resetsBefore := spy.count(t, "reset --hard origin/")

	// Let the background fill finish, including the slot job A took
	spy.openGate()
	waitForPoolSize(t, pool, 3)
	stopPool()

	if n := spy.count(t, "reset --hard origin/"); n != resetsBefore {
		t.Errorf("Background fill reset the main repo %d time(s) after startup was released", n-resetsBefore)
	}
	if branch := gitOutput(t, mainRepo, "branch", "--show-current"); branch != legacyBranch {
		t.Errorf("Expected main repo to stay on %s, got %s", legacyBranch, branch)
	}
	if _, err := os.Stat(workFile); err != nil {
		t.Errorf("Job's untracked work was removed from the main repo: %v", err)
	}
	for _, wt := range pool.ready {
		if !gitClient.WorktreeExists(wt.Path) {
			t.Errorf("Pool worktree %s is not a valid worktree", wt.Path)
		}
	}
}

// branchCreateGateScript logs every git invocation like gitSpyScript, but gates the
// pool's own `git branch nairid/pool-ready-*` call instead of its worktree-add step.
// That's the call branchMu protects during fill, so holding the gate open proves
// branchMu is actually held for the duration — exercising the exact window the
// sync fallback and stale-branch cleanup now also serialize on.
const branchCreateGateScript = `#!/bin/sh
printf '%s\n' "$*" >> "$GIT_SPY_LOG"
case "$*" in
"branch "*pool-ready*)
	while [ -e "$GIT_SPY_GATE" ]; do sleep 0.05; done
	;;
esac
exec "$GIT_SPY_REAL" "$@"
`

func installBranchCreateGate(t *testing.T) *gitSpy {
	t.Helper()

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git not found: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(branchCreateGateScript), 0755); err != nil {
		t.Fatalf("Failed to write git spy: %v", err)
	}

	spy := &gitSpy{logPath: filepath.Join(dir, "calls.log"), gatePath: filepath.Join(dir, "gate")}
	if err := os.WriteFile(spy.gatePath, nil, 0644); err != nil {
		t.Fatalf("Failed to create gate: %v", err)
	}
	t.Cleanup(spy.openGate)

	t.Setenv("GIT_SPY_LOG", spy.logPath)
	t.Setenv("GIT_SPY_GATE", spy.gatePath)
	t.Setenv("GIT_SPY_REAL", realGit)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return spy
}

// Regression for a startup burst: the pool's fill holds branchMu creating a pool-ready
// branch while jobs hit the sync fallback (pool still empty) and a stale-branch cleanup
// runs, all against the same repo. Before branchMu covered the fallback's `git worktree
// add -b` and cleanup's `git branch -D`, these raced the fill's `git branch` on
// .git/config.lock, and git does not retry — the loser's worktree setup failed outright.
// With the lock, they queue behind the fill instead of erroring.
func TestFallbackAndCleanupDuringPoolFill_NoLockErrors(t *testing.T) {
	mainRepo, tempHome, cleanup := setupTestGitRepoWithRemote(t)
	defer cleanup()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })

	appState := models.NewAppState("test-agent", filepath.Join(tempHome, "state.json"))
	appState.SetRepositoryContext(&models.RepositoryContext{
		RepoPath:   mainRepo,
		IsRepoMode: true,
	})
	gitUseCase := NewGitUseCase(gitClient, nil, appState)
	t.Setenv("AGENT_EXEC_USER", "")
	t.Setenv("HOME", tempHome)
	worktreeBase, err := gitUseCase.GetWorktreeBasePath()
	if err != nil {
		t.Fatalf("Failed to get worktree base path: %v", err)
	}

	pool := NewWorktreePool(gitClient, worktreeBase, 1)
	gitUseCase.SetWorktreePool(pool)

	// A stale nairid/ branch, untracked and not a pool branch, for CleanupStaleBranches
	// to pick up concurrently with the fill and the fallback.
	staleBranch := "nairid/stale-leftover"
	gitOutput(t, mainRepo, "branch", staleBranch)

	spy := installBranchCreateGate(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	stopPool := sync.OnceFunc(pool.Stop)
	defer stopPool()
	defer spy.openGate() // a gated git call would otherwise block Stop on failure

	// Give the fill a moment to reach the gate, holding branchMu, before the fallback
	// and cleanup try to take it too.
	deadline := time.Now().Add(5 * time.Second)
	for spy.count(t, "branch nairid/pool-ready") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("Fill never reached the gated branch-create call")
		}
		time.Sleep(10 * time.Millisecond)
	}

	jobIDs := []string{"job_a", "job_b", "job_c"}
	jobPaths := make([]string, len(jobIDs))
	var wg sync.WaitGroup
	errs := make(chan error, len(jobIDs)+1)

	for i, jobID := range jobIDs {
		wg.Add(1)
		go func(i int, jobID string) {
			defer wg.Done()
			_, path, err := gitUseCase.PrepareForNewConversationWithWorktree(jobID, "hello")
			if err != nil {
				errs <- fmt.Errorf("job %s: %w", jobID, err)
				return
			}
			jobPaths[i] = path
		}(i, jobID)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := gitUseCase.CleanupStaleBranches(); err != nil {
			errs <- fmt.Errorf("cleanup: %w", err)
		}
	}()

	// While the fill's branch-create sits at the gate, branchMu must still be held: the
	// fallback worktrees and the stale-branch delete should NOT have gone through yet.
	// Before branchMu covered them, they raced ahead of the fill instead of queuing
	// behind it, so this window would already show their result.
	time.Sleep(300 * time.Millisecond)
	for _, jobID := range jobIDs {
		if gitClient.WorktreeExists(filepath.Join(worktreeBase, jobID)) {
			t.Errorf("Job %s's worktree exists before the gated fill released branchMu — it did not wait for the lock", jobID)
		}
	}
	if branches := gitOutput(t, mainRepo, "branch", "--list", staleBranch); branches == "" {
		t.Errorf("Stale branch %s was deleted before the gated fill released branchMu — cleanup did not wait for the lock", staleBranch)
	}

	spy.openGate()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	for i, jobID := range jobIDs {
		if jobPaths[i] == "" {
			continue // already reported above via errs
		}
		if !gitClient.WorktreeExists(jobPaths[i]) {
			t.Errorf("Job %s: worktree %s was not created", jobID, jobPaths[i])
		}
	}
	if branches := gitOutput(t, mainRepo, "branch", "--list", staleBranch); branches != "" {
		t.Errorf("Expected stale branch %s to be deleted, still present: %q", staleBranch, branches)
	}
}
