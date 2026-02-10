package usecases

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"eksecd/clients"
)

// setupTestGitRepoWithRemote creates a temporary git repository with a local "remote" for testing.
// This allows testing worktree operations without needing a real remote.
func setupTestGitRepoWithRemote(t *testing.T) (mainRepo string, worktreeBase string, cleanup func()) {
	t.Helper()

	// Create temp directory for the "remote" (bare repo)
	remoteDir, err := os.MkdirTemp("", "git-remote-*")
	if err != nil {
		t.Fatalf("Failed to create remote temp dir: %v", err)
	}

	// Initialize bare repo as remote
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remoteDir
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(remoteDir)
		t.Fatalf("Failed to init bare repo: %v", err)
	}

	// Create temp directory for the main repo
	mainRepoDir, err := os.MkdirTemp("", "git-main-*")
	if err != nil {
		_ = os.RemoveAll(remoteDir)
		t.Fatalf("Failed to create main temp dir: %v", err)
	}

	// Initialize main repo
	cmd = exec.Command("git", "init")
	cmd.Dir = mainRepoDir
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(remoteDir)
		_ = os.RemoveAll(mainRepoDir)
		t.Fatalf("Failed to init main repo: %v", err)
	}

	// Configure git user
	configCmds := [][]string{
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test User"},
	}
	for _, cmdArgs := range configCmds {
		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		cmd.Dir = mainRepoDir
		if err := cmd.Run(); err != nil {
			_ = os.RemoveAll(remoteDir)
			_ = os.RemoveAll(mainRepoDir)
			t.Fatalf("Failed to configure git: %v", err)
		}
	}

	// Create initial commit
	readmePath := filepath.Join(mainRepoDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test Repo\n"), 0644); err != nil {
		_ = os.RemoveAll(remoteDir)
		_ = os.RemoveAll(mainRepoDir)
		t.Fatalf("Failed to create README: %v", err)
	}

	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = mainRepoDir
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(remoteDir)
		_ = os.RemoveAll(mainRepoDir)
		t.Fatalf("Failed to add README: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = mainRepoDir
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(remoteDir)
		_ = os.RemoveAll(mainRepoDir)
		t.Fatalf("Failed to commit: %v", err)
	}

	// Add remote
	cmd = exec.Command("git", "remote", "add", "origin", remoteDir)
	cmd.Dir = mainRepoDir
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(remoteDir)
		_ = os.RemoveAll(mainRepoDir)
		t.Fatalf("Failed to add remote: %v", err)
	}

	// Push to remote
	cmd = exec.Command("git", "push", "-u", "origin", "main")
	cmd.Dir = mainRepoDir
	if output, err := cmd.CombinedOutput(); err != nil {
		// Try master if main doesn't work
		cmd = exec.Command("git", "branch", "-M", "main")
		cmd.Dir = mainRepoDir
		_ = cmd.Run()
		cmd = exec.Command("git", "push", "-u", "origin", "main")
		cmd.Dir = mainRepoDir
		if output2, err2 := cmd.CombinedOutput(); err2 != nil {
			_ = os.RemoveAll(remoteDir)
			_ = os.RemoveAll(mainRepoDir)
			t.Fatalf("Failed to push to remote: %v\nOutput1: %s\nOutput2: %s", err2, string(output), string(output2))
		}
	}

	// Create worktree base directory
	worktreeBaseDir, err := os.MkdirTemp("", "worktree-base-*")
	if err != nil {
		_ = os.RemoveAll(remoteDir)
		_ = os.RemoveAll(mainRepoDir)
		t.Fatalf("Failed to create worktree base dir: %v", err)
	}

	cleanup = func() {
		// Prune worktrees first
		cmd := exec.Command("git", "worktree", "prune")
		cmd.Dir = mainRepoDir
		_ = cmd.Run()

		_ = os.RemoveAll(remoteDir)
		_ = os.RemoveAll(mainRepoDir)
		_ = os.RemoveAll(worktreeBaseDir)
	}

	return mainRepoDir, worktreeBaseDir, cleanup
}

func TestNewWorktreePool(t *testing.T) {
	gitClient := clients.NewGitClient()
	pool := NewWorktreePool(gitClient, "/tmp/test", 3)

	if pool == nil {
		t.Fatal("Expected pool to be created, got nil")
	}

	if pool.targetSize != 3 {
		t.Errorf("Expected target size 3, got %d", pool.targetSize)
	}

	if pool.basePath != "/tmp/test" {
		t.Errorf("Expected basePath /tmp/test, got %s", pool.basePath)
	}

	if len(pool.ready) != 0 {
		t.Errorf("Expected empty ready slice, got %d items", len(pool.ready))
	}
}

func TestGetPoolSize_Empty(t *testing.T) {
	gitClient := clients.NewGitClient()
	pool := NewWorktreePool(gitClient, "/tmp/test", 3)

	size := pool.GetPoolSize()
	if size != 0 {
		t.Errorf("Expected pool size 0, got %d", size)
	}
}

func TestGetPoolSize_WithItems(t *testing.T) {
	gitClient := clients.NewGitClient()
	pool := NewWorktreePool(gitClient, "/tmp/test", 3)

	// Manually add items to test
	pool.ready = append(pool.ready, PooledWorktree{Path: "/tmp/test1"})
	pool.ready = append(pool.ready, PooledWorktree{Path: "/tmp/test2"})

	size := pool.GetPoolSize()
	if size != 2 {
		t.Errorf("Expected pool size 2, got %d", size)
	}
}

func TestGetTargetSize(t *testing.T) {
	gitClient := clients.NewGitClient()
	pool := NewWorktreePool(gitClient, "/tmp/test", 5)

	if pool.GetTargetSize() != 5 {
		t.Errorf("Expected target size 5, got %d", pool.GetTargetSize())
	}
}

func TestAcquire_EmptyPool(t *testing.T) {
	gitClient := clients.NewGitClient()
	pool := NewWorktreePool(gitClient, "/tmp/test", 3)

	_, err := pool.Acquire("job-123", "eksecd/test-branch")
	if err == nil {
		t.Error("Expected error when acquiring from empty pool, got nil")
	}

	if !strings.Contains(err.Error(), "pool is empty") {
		t.Errorf("Expected 'pool is empty' error, got: %v", err)
	}
}

func TestAcquire_FromPool(t *testing.T) {
	mainRepo, worktreeBase, cleanup := setupTestGitRepoWithRemote(t)
	defer cleanup()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })

	// Skip if GetDefaultBranch doesn't work (local bare repos return "(unknown)")
	defaultBranch, err := gitClient.GetDefaultBranch()
	if err != nil || defaultBranch == "(unknown)" {
		t.Skip("Skipping test: GetDefaultBranch not working with local test remote")
	}

	pool := NewWorktreePool(gitClient, worktreeBase, 3)

	// Start the pool and wait for initial fill
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	// Wait for pool to fill (up to 30 seconds)
	for i := 0; i < 30; i++ {
		if pool.GetPoolSize() >= 1 {
			break
		}
		time.Sleep(time.Second)
	}

	if pool.GetPoolSize() == 0 {
		t.Fatal("Pool failed to fill within timeout")
	}

	initialSize := pool.GetPoolSize()
	t.Logf("Pool filled with %d worktrees", initialSize)

	// Acquire a worktree
	wtPath, err := pool.Acquire("job-test-123", "eksecd/test-feature")
	if err != nil {
		t.Fatalf("Failed to acquire worktree: %v", err)
	}

	// Verify path is correct
	expectedPath := filepath.Join(worktreeBase, "job-test-123")
	if wtPath != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, wtPath)
	}

	// Verify directory exists
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Error("Acquired worktree directory does not exist")
	}

	// Verify branch was renamed
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = wtPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to get branch name: %v", err)
	}

	branchName := strings.TrimSpace(string(output))
	if branchName != "eksecd/test-feature" {
		t.Errorf("Expected branch 'eksecd/test-feature', got '%s'", branchName)
	}

	// Pool size should have decreased
	newSize := pool.GetPoolSize()
	if newSize >= initialSize {
		t.Errorf("Expected pool size to decrease after acquire, was %d, now %d", initialSize, newSize)
	}

	// Stop pool
	pool.Stop()
}

func TestAcquire_InvalidWorktree(t *testing.T) {
	mainRepo, worktreeBase, cleanup := setupTestGitRepoWithRemote(t)
	defer cleanup()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })

	pool := NewWorktreePool(gitClient, worktreeBase, 3)

	// Manually inject a pooled worktree that points to a directory that exists
	// but is NOT a valid git worktree (simulates container restart where
	// .git/worktrees/ entries are lost but the volume directory persists)
	fakePath := filepath.Join(worktreeBase, "pool-fake12345")
	if err := os.MkdirAll(fakePath, 0755); err != nil {
		t.Fatalf("Failed to create fake pool dir: %v", err)
	}
	// Write a .git file pointing to a non-existent gitdir
	gitFilePath := filepath.Join(fakePath, ".git")
	if err := os.WriteFile(gitFilePath, []byte("gitdir: /nonexistent/.git/worktrees/fake"), 0644); err != nil {
		t.Fatalf("Failed to create fake .git file: %v", err)
	}

	pool.mutex.Lock()
	pool.ready = append(pool.ready, PooledWorktree{
		Path:       fakePath,
		BranchName: "eksecd/pool-ready-fake12345",
		BaseCommit: "abc123",
		CreatedAt:  time.Now(),
	})
	pool.mutex.Unlock()

	if pool.GetPoolSize() != 1 {
		t.Fatalf("Expected pool size 1, got %d", pool.GetPoolSize())
	}

	// Acquire should return ErrWorktreeInvalid instead of a generic move error
	_, err := pool.Acquire("job-test-invalid", "eksecd/test-branch")
	if err == nil {
		t.Fatal("Expected error when acquiring invalid worktree, got nil")
	}

	if !errors.Is(err, ErrWorktreeInvalid) {
		t.Errorf("Expected ErrWorktreeInvalid, got: %v", err)
	}

	// Pool should now be empty (invalid entry was discarded)
	if pool.GetPoolSize() != 0 {
		t.Errorf("Expected pool to be empty after invalid acquire, got size %d", pool.GetPoolSize())
	}

	// The fake directory should have been cleaned up
	if _, err := os.Stat(fakePath); !os.IsNotExist(err) {
		t.Error("Expected fake pool directory to be cleaned up")
	}
}

func TestConcurrentAcquire(t *testing.T) {
	mainRepo, worktreeBase, cleanup := setupTestGitRepoWithRemote(t)
	defer cleanup()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })

	// Skip if GetDefaultBranch doesn't work (local bare repos return "(unknown)")
	defaultBranch, err := gitClient.GetDefaultBranch()
	if err != nil || defaultBranch == "(unknown)" {
		t.Skip("Skipping test: GetDefaultBranch not working with local test remote")
	}

	pool := NewWorktreePool(gitClient, worktreeBase, 5)

	// Start the pool and wait for initial fill
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	// Wait for pool to fill
	for i := 0; i < 60; i++ {
		if pool.GetPoolSize() >= 3 {
			break
		}
		time.Sleep(time.Second)
	}

	if pool.GetPoolSize() < 3 {
		t.Fatalf("Pool only filled to %d, need at least 3", pool.GetPoolSize())
	}

	// Launch concurrent acquires
	var wg sync.WaitGroup
	results := make(chan string, 3)
	errors := make(chan error, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			jobID := filepath.Base(worktreeBase) + "-concurrent-" + string(rune('a'+idx))
			branchName := "eksecd/concurrent-" + string(rune('a'+idx))
			path, err := pool.Acquire(jobID, branchName)
			if err != nil {
				errors <- err
			} else {
				results <- path
			}
		}(i)
	}

	wg.Wait()
	close(results)
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent acquire error: %v", err)
	}

	// Verify unique paths
	paths := make(map[string]bool)
	for path := range results {
		if paths[path] {
			t.Errorf("Duplicate path returned: %s", path)
		}
		paths[path] = true
	}

	pool.Stop()
}

func TestReplenish_AfterAcquire(t *testing.T) {
	mainRepo, worktreeBase, cleanup := setupTestGitRepoWithRemote(t)
	defer cleanup()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })

	// Skip if GetDefaultBranch doesn't work (local bare repos return "(unknown)")
	defaultBranch, err := gitClient.GetDefaultBranch()
	if err != nil || defaultBranch == "(unknown)" {
		t.Skip("Skipping test: GetDefaultBranch not working with local test remote")
	}

	pool := NewWorktreePool(gitClient, worktreeBase, 2)

	// Start the pool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	// Wait for initial fill
	for i := 0; i < 30; i++ {
		if pool.GetPoolSize() >= 2 {
			break
		}
		time.Sleep(time.Second)
	}

	if pool.GetPoolSize() < 2 {
		t.Fatalf("Pool only filled to %d, expected 2", pool.GetPoolSize())
	}

	// Acquire one
	_, acquireErr := pool.Acquire("job-replenish-test", "eksecd/replenish-test")
	if acquireErr != nil {
		t.Fatalf("Failed to acquire: %v", acquireErr)
	}

	// Wait for replenishment
	for i := 0; i < 30; i++ {
		if pool.GetPoolSize() >= 2 {
			break
		}
		time.Sleep(time.Second)
	}

	// Pool should be back to target size
	if pool.GetPoolSize() < 2 {
		t.Errorf("Pool did not replenish to target size, got %d", pool.GetPoolSize())
	}

	pool.Stop()
}

func TestCleanupPool(t *testing.T) {
	mainRepo, worktreeBase, cleanup := setupTestGitRepoWithRemote(t)
	defer cleanup()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })

	// Skip if GetDefaultBranch doesn't work (local bare repos return "(unknown)")
	defaultBranch, err := gitClient.GetDefaultBranch()
	if err != nil || defaultBranch == "(unknown)" {
		t.Skip("Skipping test: GetDefaultBranch not working with local test remote")
	}

	pool := NewWorktreePool(gitClient, worktreeBase, 2)

	// Start and fill pool
	ctx, cancel := context.WithCancel(context.Background())
	pool.Start(ctx)

	// Wait for fill
	for i := 0; i < 30; i++ {
		if pool.GetPoolSize() >= 2 {
			break
		}
		time.Sleep(time.Second)
	}

	// Stop and cleanup
	cancel()
	pool.Stop()

	// Get paths before cleanup
	pool.mutex.Lock()
	paths := make([]string, len(pool.ready))
	for i, wt := range pool.ready {
		paths[i] = wt.Path
	}
	pool.mutex.Unlock()

	cleanupErr := pool.CleanupPool()
	if cleanupErr != nil {
		t.Errorf("CleanupPool returned error: %v", cleanupErr)
	}

	// Verify pool is empty
	if pool.GetPoolSize() != 0 {
		t.Errorf("Expected empty pool after cleanup, got %d", pool.GetPoolSize())
	}

	// Verify directories are removed
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("Worktree directory still exists after cleanup: %s", path)
		}
	}
}

func TestReclaimOrphanedPoolWorktrees(t *testing.T) {
	mainRepo, worktreeBase, cleanup := setupTestGitRepoWithRemote(t)
	defer cleanup()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })

	// Create a pool worktree manually (simulating crash recovery)
	poolPath := filepath.Join(worktreeBase, "pool-orphan123")
	branchName := "eksecd/pool-ready-orphan123"

	// Use "main" directly since we set up the test repo with main branch
	// (GetDefaultBranch may return "(unknown)" for local test remotes)
	baseRef := "origin/main"
	err := gitClient.CreateWorktree(poolPath, branchName, baseRef)
	if err != nil {
		t.Fatalf("Failed to create orphan worktree: %v", err)
	}

	// Create pool and reclaim
	pool := NewWorktreePool(gitClient, worktreeBase, 3)

	err = pool.ReclaimOrphanedPoolWorktrees()
	if err != nil {
		t.Fatalf("ReclaimOrphanedPoolWorktrees failed: %v", err)
	}

	// Verify worktree was reclaimed
	if pool.GetPoolSize() != 1 {
		t.Errorf("Expected 1 reclaimed worktree, got %d", pool.GetPoolSize())
	}

	// Verify it's the correct one
	pool.mutex.Lock()
	if len(pool.ready) > 0 && pool.ready[0].Path != poolPath {
		t.Errorf("Expected reclaimed path %s, got %s", poolPath, pool.ready[0].Path)
	}
	pool.mutex.Unlock()

	// Cleanup
	_ = pool.CleanupPool()
}

func TestCleanupStaleJobWorktrees(t *testing.T) {
	mainRepo, worktreeBase, cleanup := setupTestGitRepoWithRemote(t)
	defer cleanup()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })

	// Create a valid job worktree first
	validJobPath := filepath.Join(worktreeBase, "j_valid123")
	branchName := "eksecd/test-branch-valid"
	baseRef := "origin/main"
	err := gitClient.CreateWorktree(validJobPath, branchName, baseRef)
	if err != nil {
		t.Fatalf("Failed to create valid worktree: %v", err)
	}

	// Create a broken job worktree (directory exists but .git points to non-existent path)
	brokenJobPath := filepath.Join(worktreeBase, "j_broken456")
	if err := os.MkdirAll(brokenJobPath, 0755); err != nil {
		t.Fatalf("Failed to create broken job directory: %v", err)
	}
	// Create a .git file pointing to a non-existent gitdir
	gitFilePath := filepath.Join(brokenJobPath, ".git")
	if err := os.WriteFile(gitFilePath, []byte("gitdir: /nonexistent/path/.git/worktrees/broken"), 0644); err != nil {
		t.Fatalf("Failed to create broken .git file: %v", err)
	}

	// Create pool and run cleanup
	pool := NewWorktreePool(gitClient, worktreeBase, 3)

	err = pool.CleanupStaleJobWorktrees()
	if err != nil {
		t.Fatalf("CleanupStaleJobWorktrees failed: %v", err)
	}

	// Verify valid worktree still exists
	if _, err := os.Stat(validJobPath); os.IsNotExist(err) {
		t.Error("Valid job worktree was incorrectly removed")
	}

	// Verify broken worktree was removed
	if _, err := os.Stat(brokenJobPath); !os.IsNotExist(err) {
		t.Error("Broken job worktree was not removed")
	}

	// Cleanup
	_ = gitClient.RemoveWorktree(validJobPath)
	_ = gitClient.DeleteLocalBranch(branchName)
}

func TestStartAndStop(t *testing.T) {
	gitClient := clients.NewGitClient()
	pool := NewWorktreePool(gitClient, "/tmp/nonexistent", 1)

	ctx, cancel := context.WithCancel(context.Background())

	// Start should not block
	done := make(chan struct{})
	go func() {
		pool.Start(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Start returned immediately (goroutine launched)
	case <-time.After(time.Second):
		// This is fine - Start doesn't return, it just launches a goroutine
	}

	// Give it a moment to start the loop
	time.Sleep(100 * time.Millisecond)

	// Stop should complete
	cancel()
	stopDone := make(chan struct{})
	go func() {
		pool.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		// Good
	case <-time.After(5 * time.Second):
		t.Error("Stop did not complete within timeout")
	}
}

func TestResetMainRepoToDefaultBranch(t *testing.T) {
	mainRepo, worktreeBase, cleanup := setupTestGitRepoWithRemote(t)
	defer cleanup()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })

	// Skip if GetDefaultBranch doesn't work (local bare repos return "(unknown)")
	defaultBranch, err := gitClient.GetDefaultBranch()
	if err != nil || defaultBranch == "(unknown)" {
		t.Skip("Skipping test: GetDefaultBranch not working with local test remote")
	}

	pool := NewWorktreePool(gitClient, worktreeBase, 1)

	// Create a feature branch and switch to it
	featureBranch := "feature/test-branch"
	cmd := exec.Command("git", "checkout", "-b", featureBranch)
	cmd.Dir = mainRepo
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create feature branch: %v", err)
	}

	// Create some uncommitted changes
	testFile := filepath.Join(mainRepo, "dirty-file.txt")
	if err := os.WriteFile(testFile, []byte("uncommitted content"), 0644); err != nil {
		t.Fatalf("Failed to create dirty file: %v", err)
	}

	// Verify we're on the feature branch with uncommitted changes
	cmd = exec.Command("git", "branch", "--show-current")
	cmd.Dir = mainRepo
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to get current branch: %v", err)
	}
	currentBranch := strings.TrimSpace(string(output))
	if currentBranch != featureBranch {
		t.Fatalf("Expected to be on %s, got %s", featureBranch, currentBranch)
	}

	// Call resetMainRepoToDefaultBranch
	err = pool.resetMainRepoToDefaultBranch()
	if err != nil {
		t.Fatalf("resetMainRepoToDefaultBranch failed: %v", err)
	}

	// Verify we're back on the default branch
	cmd = exec.Command("git", "branch", "--show-current")
	cmd.Dir = mainRepo
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to get current branch after reset: %v", err)
	}
	currentBranch = strings.TrimSpace(string(output))
	if currentBranch != defaultBranch {
		t.Errorf("Expected to be on %s after reset, got %s", defaultBranch, currentBranch)
	}

	// Verify uncommitted changes were cleaned
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = mainRepo
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to get git status: %v", err)
	}
	if strings.TrimSpace(string(output)) != "" {
		t.Errorf("Expected clean working directory after reset, got: %s", string(output))
	}
}

func TestReplenish_ResetsMainRepoBeforeCreatingWorktree(t *testing.T) {
	mainRepo, worktreeBase, cleanup := setupTestGitRepoWithRemote(t)
	defer cleanup()

	gitClient := clients.NewGitClient()
	gitClient.SetRepoPathProvider(func() string { return mainRepo })

	// Skip if GetDefaultBranch doesn't work (local bare repos return "(unknown)")
	defaultBranch, err := gitClient.GetDefaultBranch()
	if err != nil || defaultBranch == "(unknown)" {
		t.Skip("Skipping test: GetDefaultBranch not working with local test remote")
	}

	// Create a feature branch and switch to it (simulating a dirty main repo state)
	featureBranch := "feature/dirty-state"
	cmd := exec.Command("git", "checkout", "-b", featureBranch)
	cmd.Dir = mainRepo
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create feature branch: %v", err)
	}

	// Add some uncommitted changes to make the repo "dirty"
	testFile := filepath.Join(mainRepo, "uncommitted-change.txt")
	if err := os.WriteFile(testFile, []byte("this should not leak"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	pool := NewWorktreePool(gitClient, worktreeBase, 1)

	// Start the pool - this will call replenish() which should reset the main repo
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	// Wait for pool to fill
	for i := 0; i < 30; i++ {
		if pool.GetPoolSize() >= 1 {
			break
		}
		time.Sleep(time.Second)
	}

	if pool.GetPoolSize() == 0 {
		t.Fatal("Pool failed to fill within timeout")
	}

	// After replenish, the main repo should be on the default branch
	cmd = exec.Command("git", "branch", "--show-current")
	cmd.Dir = mainRepo
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to get current branch: %v", err)
	}
	currentBranch := strings.TrimSpace(string(output))
	if currentBranch != defaultBranch {
		t.Errorf("Expected main repo to be on %s after replenish, got %s", defaultBranch, currentBranch)
	}

	// And should have no uncommitted changes
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = mainRepo
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to get git status: %v", err)
	}
	if strings.TrimSpace(string(output)) != "" {
		t.Errorf("Expected clean working directory after replenish, got: %s", string(output))
	}

	pool.Stop()
	_ = pool.CleanupPool()
}
