package clients

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestGitRepo creates a temporary git repository for testing
func setupTestGitRepo(t *testing.T) (string, func()) {
	t.Helper()

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "git-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("Failed to init git repo: %v", err)
	}

	// Configure git user for commits
	configCmds := [][]string{
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test User"},
	}
	for _, cmdArgs := range configCmds {
		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		cmd.Dir = tmpDir
		if err := cmd.Run(); err != nil {
			_ = os.RemoveAll(tmpDir)
			t.Fatalf("Failed to configure git: %v", err)
		}
	}

	// Create initial commit
	readmePath := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test Repo\n"), 0644); err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("Failed to create README: %v", err)
	}

	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("Failed to add README: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("Failed to commit: %v", err)
	}

	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}

	return tmpDir, cleanup
}

func TestRemoteBranchExists_NonExistentRemote(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	// Test checking for a remote branch when no remote is configured
	// This should return an error since git ls-remote needs a remote
	_, err := client.RemoteBranchExists("some-branch")
	if err == nil {
		t.Error("Expected error when checking remote branch without remote configured, got nil")
	}
}

func TestRemoteBranchExists_WithRemote(t *testing.T) {
	// This test requires a real git repository with remote
	// Skip if not in a git repo or if origin remote doesn't exist
	cmd := exec.Command("git", "remote", "get-url", "origin")
	if err := cmd.Run(); err != nil {
		t.Skip("Skipping test: no origin remote configured")
	}

	client := NewGitClient()
	// Use current directory (should be in nairid repo)
	client.SetRepoPathProvider(func() string { return "" })

	// Test with a branch name that likely doesn't exist
	exists, err := client.RemoteBranchExists("nonexistent-branch-12345-test")
	if err != nil {
		t.Fatalf("Unexpected error checking remote branch: %v", err)
	}

	if exists {
		t.Error("Expected nonexistent branch to return false, got true")
	}
}

func TestGetLocalBranches_EmptyRepo(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	branches, err := client.GetLocalBranches()
	if err != nil {
		t.Fatalf("Failed to get local branches: %v", err)
	}

	// Should have at least the main/master branch
	if len(branches) == 0 {
		t.Error("Expected at least one branch, got 0")
	}

	// Check that main or master is in the list
	hasDefaultBranch := false
	for _, branch := range branches {
		if branch == "main" || branch == "master" {
			hasDefaultBranch = true
			break
		}
	}

	if !hasDefaultBranch {
		t.Errorf("Expected main or master branch in list, got: %v", branches)
	}
}

func TestGetLocalBranches_WithMultipleBranches(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	// Create additional branches
	branchNames := []string{"feature/test", "bugfix/issue-123"}
	for _, branchName := range branchNames {
		cmd := exec.Command("git", "branch", branchName)
		cmd.Dir = repoPath
		if err := cmd.Run(); err != nil {
			t.Fatalf("Failed to create branch %s: %v", branchName, err)
		}
	}

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	branches, err := client.GetLocalBranches()
	if err != nil {
		t.Fatalf("Failed to get local branches: %v", err)
	}

	// Should have at least 3 branches (main/master + 2 created)
	if len(branches) < 3 {
		t.Errorf("Expected at least 3 branches, got %d", len(branches))
	}

	// Check that our created branches are in the list
	for _, expectedBranch := range branchNames {
		found := false
		for _, branch := range branches {
			if branch == expectedBranch {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected branch %s in list, got: %v", expectedBranch, branches)
		}
	}
}

func TestCheckoutBranch_ExistingBranch(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	// Create a test branch
	branchName := "test-branch"
	cmd := exec.Command("git", "branch", branchName)
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create branch: %v", err)
	}

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	// Checkout the branch
	err := client.CheckoutBranch(branchName)
	if err != nil {
		t.Fatalf("Failed to checkout branch: %v", err)
	}

	// Verify we're on the correct branch
	cmd = exec.Command("git", "branch", "--show-current")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get current branch: %v", err)
	}

	currentBranch := strings.TrimSpace(string(output))
	if currentBranch != branchName {
		t.Errorf("Expected current branch to be %s, got %s", branchName, currentBranch)
	}
}

func TestCheckoutBranch_NonExistentBranch(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	// Try to checkout non-existent branch
	err := client.CheckoutBranch("nonexistent-branch")
	if err == nil {
		t.Error("Expected error when checking out non-existent branch, got nil")
	}

	if !strings.Contains(err.Error(), "git checkout failed") {
		t.Errorf("Expected 'git checkout failed' in error, got: %v", err)
	}
}

func TestDeleteLocalBranch_ExistingBranch(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	// Create and then delete a branch
	branchName := "branch-to-delete"
	cmd := exec.Command("git", "branch", branchName)
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create branch: %v", err)
	}

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	// Delete the branch
	err := client.DeleteLocalBranch(branchName)
	if err != nil {
		t.Fatalf("Failed to delete branch: %v", err)
	}

	// Verify branch is gone
	branches, err := client.GetLocalBranches()
	if err != nil {
		t.Fatalf("Failed to get local branches: %v", err)
	}

	for _, branch := range branches {
		if branch == branchName {
			t.Errorf("Branch %s should have been deleted but still exists", branchName)
		}
	}
}

func TestDeleteLocalBranch_NonExistentBranch(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	// Try to delete non-existent branch
	err := client.DeleteLocalBranch("nonexistent-branch")
	if err == nil {
		t.Error("Expected error when deleting non-existent branch, got nil")
	}
}

// ============ isRateLimitError Tests ============

func TestIsRateLimitError_NilError(t *testing.T) {
	if isRateLimitError(nil, "") {
		t.Error("Expected false for nil error")
	}
}

func TestIsRateLimitError_RateLimitErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    string
		output string
	}{
		{
			"graphql rate limit in output",
			"exit status 1",
			"GraphQL: API rate limit already exceeded for installation ID 92312766.",
		},
		{
			"rate limit in error",
			"API rate limit exceeded",
			"",
		},
		{
			"secondary rate limit in output",
			"exit status 1",
			"You have exceeded a secondary rate limit",
		},
		{
			"abuse detection in output",
			"exit status 1",
			"You have triggered an abuse detection mechanism",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !isRateLimitError(fmt.Errorf("%s", tc.err), tc.output) {
				t.Errorf("Expected rate limit for err=%q output=%q", tc.err, tc.output)
			}
		})
	}
}

func TestIsRateLimitError_NonRateLimitErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    string
		output string
	}{
		{"timeout", "connection timeout", ""},
		{"generic error", "something went wrong", ""},
		{"not found", "exit status 1", "not found"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if isRateLimitError(fmt.Errorf("%s", tc.err), tc.output) {
				t.Errorf("Expected non-rate-limit for err=%q output=%q", tc.err, tc.output)
			}
		})
	}
}

// ============ isRecoverableGHError Tests ============

func TestIsRecoverableGHError_NilError(t *testing.T) {
	if isRecoverableGHError(nil, "") {
		t.Error("Expected false for nil error")
	}
}

func TestIsRecoverableGHError_NetworkErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    string
		output string
	}{
		{"timeout in error", "connection timeout", ""},
		{"dial tcp in error", "dial tcp 1.2.3.4:443: i/o timeout", ""},
		{"context deadline in error", "context deadline exceeded", ""},
		{"timeout in output", "exit status 1", "i/o timeout"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !isRecoverableGHError(fmt.Errorf("%s", tc.err), tc.output) {
				t.Errorf("Expected recoverable for err=%q output=%q", tc.err, tc.output)
			}
		})
	}
}

func TestIsRecoverableGHError_RateLimitErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    string
		output string
	}{
		{
			"graphql rate limit in output",
			"exit status 1",
			"GraphQL: API rate limit already exceeded for installation ID 92312766.",
		},
		{
			"rate limit in error",
			"API rate limit exceeded",
			"",
		},
		{
			"secondary rate limit in output",
			"exit status 1",
			"You have exceeded a secondary rate limit",
		},
		{
			"abuse detection in output",
			"exit status 1",
			"You have triggered an abuse detection mechanism",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !isRecoverableGHError(fmt.Errorf("%s", tc.err), tc.output) {
				t.Errorf("Expected recoverable for err=%q output=%q", tc.err, tc.output)
			}
		})
	}
}

func TestIsRecoverableGHError_NonRecoverableErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    string
		output string
	}{
		{"generic error", "something went wrong", ""},
		{"not found", "exit status 1", "not found"},
		{"permission denied", "exit status 1", "permission denied"},
		{"invalid branch", "exit status 128", "fatal: invalid reference"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if isRecoverableGHError(fmt.Errorf("%s", tc.err), tc.output) {
				t.Errorf("Expected non-recoverable for err=%q output=%q", tc.err, tc.output)
			}
		})
	}
}

// ============ Worktree Tests ============

func TestCreateWorktree(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	// Create a worktree directory
	worktreePath := filepath.Join(os.TempDir(), "worktree-test-"+t.Name())
	defer func() { _ = os.RemoveAll(worktreePath) }()

	branchName := "test-worktree-branch"
	err := client.CreateWorktree(worktreePath, branchName, "")
	if err != nil {
		t.Fatalf("Failed to create worktree: %v", err)
	}

	// Verify worktree exists
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		t.Error("Worktree directory was not created")
	}

	// Verify it contains git files
	gitFile := filepath.Join(worktreePath, ".git")
	if _, err := os.Stat(gitFile); os.IsNotExist(err) {
		t.Error("Worktree .git file was not created")
	}

	// Verify branch was created
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = worktreePath
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get current branch in worktree: %v", err)
	}

	currentBranch := strings.TrimSpace(string(output))
	if currentBranch != branchName {
		t.Errorf("Expected worktree branch to be %s, got %s", branchName, currentBranch)
	}
}

func TestCreateWorktree_ExistingBranch(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	// First create a branch
	branchName := "existing-branch"
	cmd := exec.Command("git", "branch", branchName)
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create branch: %v", err)
	}

	// Try to create worktree with existing branch name - should fail
	worktreePath := filepath.Join(os.TempDir(), "worktree-test-"+t.Name())
	defer func() { _ = os.RemoveAll(worktreePath) }()

	err := client.CreateWorktree(worktreePath, branchName, "")
	if err == nil {
		t.Error("Expected error when creating worktree with existing branch name, got nil")
	}
}

func TestRemoveWorktree(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	// Create a worktree first
	worktreePath := filepath.Join(os.TempDir(), "worktree-test-"+t.Name())
	branchName := "worktree-to-remove"

	err := client.CreateWorktree(worktreePath, branchName, "")
	if err != nil {
		t.Fatalf("Failed to create worktree: %v", err)
	}

	// Verify worktree exists
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		t.Fatal("Worktree was not created")
	}

	// Remove the worktree
	err = client.RemoveWorktree(worktreePath)
	if err != nil {
		t.Fatalf("Failed to remove worktree: %v", err)
	}

	// Verify worktree directory is gone
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Error("Worktree directory should be removed")
	}
}

func TestListWorktrees(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	// Initially should have just the main worktree
	worktrees, err := client.ListWorktrees()
	if err != nil {
		t.Fatalf("Failed to list worktrees: %v", err)
	}

	if len(worktrees) != 1 {
		t.Errorf("Expected 1 worktree (main), got %d", len(worktrees))
	}

	// Create additional worktrees
	worktreePaths := []string{
		filepath.Join(os.TempDir(), "worktree-test-1-"+t.Name()),
		filepath.Join(os.TempDir(), "worktree-test-2-"+t.Name()),
	}
	for i, wtPath := range worktreePaths {
		defer func() { _ = os.RemoveAll(wtPath) }()
		branchName := "test-branch-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + string(rune('a'+i))
		if err := client.CreateWorktree(wtPath, branchName, ""); err != nil {
			t.Fatalf("Failed to create worktree %s: %v", wtPath, err)
		}
	}

	// List again
	worktrees, err = client.ListWorktrees()
	if err != nil {
		t.Fatalf("Failed to list worktrees after creation: %v", err)
	}

	if len(worktrees) != 3 {
		t.Errorf("Expected 3 worktrees, got %d", len(worktrees))
	}
}

func TestWorktreeExists(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	// Non-existent worktree should return false
	nonExistentPath := filepath.Join(os.TempDir(), "nonexistent-worktree-"+t.Name())
	if client.WorktreeExists(nonExistentPath) {
		t.Error("WorktreeExists should return false for non-existent path")
	}

	// Create a worktree
	worktreePath := filepath.Join(os.TempDir(), "worktree-exists-test-"+t.Name())
	defer func() { _ = os.RemoveAll(worktreePath) }()
	branchName := "worktree-exists-branch"

	if err := client.CreateWorktree(worktreePath, branchName, ""); err != nil {
		t.Fatalf("Failed to create worktree: %v", err)
	}

	// Now it should exist
	if !client.WorktreeExists(worktreePath) {
		t.Error("WorktreeExists should return true for existing worktree")
	}
}

func TestPruneWorktrees(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	// Create a worktree
	worktreePath := filepath.Join(os.TempDir(), "worktree-prune-test-"+t.Name())
	branchName := "worktree-prune-branch"

	if err := client.CreateWorktree(worktreePath, branchName, ""); err != nil {
		t.Fatalf("Failed to create worktree: %v", err)
	}

	// Manually delete the worktree directory (simulating stale worktree)
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatalf("Failed to manually remove worktree dir: %v", err)
	}

	// Prune should clean up the stale entry
	err := client.PruneWorktrees()
	if err != nil {
		t.Fatalf("Failed to prune worktrees: %v", err)
	}

	// List worktrees - should only have main
	worktrees, err := client.ListWorktrees()
	if err != nil {
		t.Fatalf("Failed to list worktrees after prune: %v", err)
	}

	if len(worktrees) != 1 {
		t.Errorf("Expected 1 worktree after prune (main only), got %d", len(worktrees))
	}
}

func TestWorktreeGitOperations(t *testing.T) {
	repoPath, cleanup := setupTestGitRepo(t)
	defer cleanup()

	client := NewGitClient()
	client.SetRepoPathProvider(func() string { return repoPath })

	// Create a worktree
	worktreePath := filepath.Join(os.TempDir(), "worktree-ops-test-"+t.Name())
	defer func() { _ = os.RemoveAll(worktreePath) }()
	branchName := "worktree-ops-branch"

	if err := client.CreateWorktree(worktreePath, branchName, ""); err != nil {
		t.Fatalf("Failed to create worktree: %v", err)
	}

	// Test AddAllInWorktree
	testFile := filepath.Join(worktreePath, "test-file.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	if err := client.AddAllInWorktree(worktreePath); err != nil {
		t.Fatalf("Failed to add all in worktree: %v", err)
	}

	// Verify file is staged
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = worktreePath
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get git status: %v", err)
	}

	if !strings.Contains(string(output), "A  test-file.txt") {
		t.Errorf("Expected test-file.txt to be staged, got: %s", string(output))
	}

	// Test CommitInWorktree
	if err := client.CommitInWorktree(worktreePath, "Test commit"); err != nil {
		t.Fatalf("Failed to commit in worktree: %v", err)
	}

	// Verify commit was made
	cmd = exec.Command("git", "log", "--oneline", "-1")
	cmd.Dir = worktreePath
	output, err = cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get git log: %v", err)
	}

	if !strings.Contains(string(output), "Test commit") {
		t.Errorf("Expected commit message 'Test commit', got: %s", string(output))
	}

	// Test HasUncommittedChangesInWorktree (should be false after commit)
	hasChanges, err := client.HasUncommittedChangesInWorktree(worktreePath)
	if err != nil {
		t.Fatalf("Failed to check for uncommitted changes: %v", err)
	}
	if hasChanges {
		t.Error("Expected no uncommitted changes after commit")
	}

	// Make another change to test HasUncommittedChanges returning true
	if err := os.WriteFile(testFile, []byte("modified content"), 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	hasChanges, err = client.HasUncommittedChangesInWorktree(worktreePath)
	if err != nil {
		t.Fatalf("Failed to check for uncommitted changes: %v", err)
	}
	if !hasChanges {
		t.Error("Expected uncommitted changes after modifying file")
	}
}
