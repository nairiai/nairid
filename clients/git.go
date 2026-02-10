package clients

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"eksecd/core/log"

	"github.com/cenkalti/backoff/v4"
)

type GitClient struct {
	getRepoPath func() string // Function to get repository path (allows lazy evaluation)
}

// WorktreeInfo contains information about a git worktree
type WorktreeInfo struct {
	Path   string
	Branch string
	Commit string
}

func NewGitClient() *GitClient {
	return &GitClient{
		getRepoPath: func() string { return "" }, // Default: no repo path
	}
}

// SetRepoPathProvider sets the function that provides the repository path
func (g *GitClient) SetRepoPathProvider(provider func() string) {
	g.getRepoPath = provider
}

// setWorkDir sets the working directory for a git command if a repo path is configured
func (g *GitClient) setWorkDir(cmd *exec.Cmd) {
	if repoPath := g.getRepoPath(); repoPath != "" {
		cmd.Dir = repoPath
	}
}

// isRateLimitError checks if an error is a GitHub API rate limit error
func isRateLimitError(err error, output string) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	outputStr := strings.ToLower(output)

	rateLimitPatterns := []string{
		"rate limit",
		"secondary rate limit",
		"abuse detection",
	}

	for _, pattern := range rateLimitPatterns {
		if strings.Contains(errStr, pattern) || strings.Contains(outputStr, pattern) {
			return true
		}
	}

	return false
}

// isRecoverableGHError checks if an error is a recoverable GitHub API error that should be retried
func isRecoverableGHError(err error, output string) bool {
	if err == nil {
		return false
	}

	if isRateLimitError(err, output) {
		return true
	}

	errStr := strings.ToLower(err.Error())
	outputStr := strings.ToLower(output)

	recoverablePatterns := []string{
		"timeout",
		"i/o timeout",
		"connection timeout",
		"dial tcp",
		"context deadline exceeded",
	}

	for _, pattern := range recoverablePatterns {
		if strings.Contains(errStr, pattern) || strings.Contains(outputStr, pattern) {
			return true
		}
	}

	return false
}

// executeWithRetry executes a GitHub CLI command with exponential backoff for recoverable errors.
// Rate limit errors use a longer backoff (up to 10 minutes) since GitHub rate limits reset on longer windows.
func (g *GitClient) executeWithRetry(cmd *exec.Cmd, operationName string) ([]byte, error) {
	var output []byte
	var err error
	var rateLimitDetected bool

	retryBackoff := backoff.NewExponentialBackOff()
	retryBackoff.InitialInterval = 2 * time.Second
	retryBackoff.MaxInterval = 30 * time.Second
	retryBackoff.MaxElapsedTime = 2 * time.Minute
	retryBackoff.Multiplier = 2

	// Preserve original working directory for retries
	originalDir := cmd.Dir

	retryOperation := func() error {
		output, err = cmd.CombinedOutput()

		if err != nil && isRecoverableGHError(err, string(output)) {
			// On first rate limit detection, extend the backoff parameters
			if !rateLimitDetected && isRateLimitError(err, string(output)) {
				rateLimitDetected = true
				retryBackoff.MaxInterval = 60 * time.Second
				retryBackoff.MaxElapsedTime = 10 * time.Minute
				log.Info("⏳ GitHub API rate limit detected for %s, extending retry window to 10 minutes...", operationName)
			} else {
				log.Info("⏳ GitHub API recoverable error detected for %s, retrying...", operationName)
			}
			// Reset command for retry, preserving working directory
			cmd = exec.Command(cmd.Args[0], cmd.Args[1:]...)
			cmd.Dir = originalDir
			return err // This will trigger a retry
		}

		return nil // Success or non-recoverable error, stop retrying
	}

	retryErr := backoff.Retry(retryOperation, retryBackoff)
	if retryErr != nil {
		// If we still have the original error, use it
		if err != nil {
			return output, err
		}
		return output, retryErr
	}

	return output, err
}

func (g *GitClient) CheckoutBranch(branchName string) error {
	log.Info("📋 Starting to checkout branch: %s", branchName)

	cmd := exec.Command("git", "checkout", branchName)
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Git checkout failed for branch %s: %v\nOutput: %s", branchName, err, string(output))
		return fmt.Errorf("git checkout failed: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully checked out branch: %s", branchName)
	log.Info("📋 Completed successfully - checked out branch")
	return nil
}

func (g *GitClient) PullLatest() error {
	log.Info("📋 Starting to pull latest changes")

	cmd := exec.Command("git", "pull")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		outputStr := strings.ToLower(string(output))

		// Check if error is due to no upstream branch configured
		// This happens when branch hasn't been pushed to remote yet
		if strings.Contains(outputStr, "no tracking information") ||
			strings.Contains(outputStr, "no upstream branch") ||
			strings.Contains(outputStr, "there is no tracking information for the current branch") {
			log.Info("ℹ️ No remote branch exists yet - nothing to pull")
			log.Info("📋 Completed successfully - no remote branch to pull from")
			return nil
		}

		// Check if error is due to remote branch being deleted
		// This happens when branch was pushed but later deleted remotely (e.g., after PR merge)
		if strings.Contains(outputStr, "no such ref was fetched") ||
			strings.Contains(outputStr, "couldn't find remote ref") {
			// Check if branch was ever pushed (has upstream tracking)
			hasPushed, trackErr := g.HasUpstreamTracking()
			if trackErr != nil {
				log.Warn("⚠️ Failed to check upstream tracking: %v", trackErr)
			}

			if !hasPushed {
				// Branch was never pushed - this is not an error, just nothing to pull
				log.Info("ℹ️ Branch has no upstream - never pushed, nothing to pull")
				return nil
			}

			// Branch was pushed but now deleted on remote
			log.Warn("⚠️ Remote branch has been deleted - this may indicate the PR was merged or branch was manually removed")
			return fmt.Errorf("remote branch deleted: %w", err)
		}

		log.Error("❌ Git pull failed: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("git pull failed: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully pulled latest changes")
	log.Info("📋 Completed successfully - pulled latest changes")
	return nil
}

func (g *GitClient) ResetHard() error {
	log.Info("📋 Starting to reset hard to HEAD")

	// Check if repository has any commits first
	// In a fresh repository with no commits, HEAD doesn't exist and reset will fail
	hasCommits, err := g.hasCommits()
	if err != nil {
		log.Error("❌ Failed to check if repository has commits: %v", err)
		return fmt.Errorf("failed to check if repository has commits: %w", err)
	}

	if !hasCommits {
		log.Info("ℹ️ Repository has no commits yet - skipping reset")
		log.Info("📋 Completed successfully - skipped reset (empty repository)")
		return nil
	}

	cmd := exec.Command("git", "reset", "--hard", "HEAD")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Git reset hard failed: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("git reset hard failed: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully reset hard to HEAD")
	log.Info("📋 Completed successfully - reset hard")
	return nil
}

// hasCommits checks if the repository has any commits
func (g *GitClient) hasCommits() (bool, error) {
	// Use git rev-parse --verify HEAD to check if HEAD exists
	// This will return an error if there are no commits
	cmd := exec.Command("git", "rev-parse", "--verify", "HEAD")
	g.setWorkDir(cmd)
	err := cmd.Run()

	// If command succeeds, HEAD exists (repository has commits)
	// If it fails, HEAD doesn't exist (empty repository)
	return err == nil, nil
}

// HasUpstreamTracking checks if the current branch has upstream tracking configured.
// Returns true if the branch has been pushed and is tracking a remote branch.
func (g *GitClient) HasUpstreamTracking() (bool, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "@{u}")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		outputStr := strings.ToLower(string(output))
		// These errors indicate no upstream is configured (branch never pushed)
		if strings.Contains(outputStr, "no upstream") ||
			strings.Contains(outputStr, "does not point to a branch") ||
			strings.Contains(outputStr, "no such branch") {
			return false, nil
		}
		// Other errors are actual failures
		return false, fmt.Errorf("failed to check upstream tracking: %w", err)
	}

	// If we got output without error, upstream exists
	return strings.TrimSpace(string(output)) != "", nil
}

// HasUpstreamTrackingInWorktree checks if the current branch in a worktree has upstream tracking.
func (g *GitClient) HasUpstreamTrackingInWorktree(worktreePath string) (bool, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "@{u}")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		outputStr := strings.ToLower(string(output))
		// These errors indicate no upstream is configured (branch never pushed)
		if strings.Contains(outputStr, "no upstream") ||
			strings.Contains(outputStr, "does not point to a branch") ||
			strings.Contains(outputStr, "no such branch") {
			return false, nil
		}
		// Other errors are actual failures
		return false, fmt.Errorf("failed to check upstream tracking in worktree: %w", err)
	}

	// If we got output without error, upstream exists
	return strings.TrimSpace(string(output)) != "", nil
}

func (g *GitClient) CleanUntracked() error {
	log.Info("📋 Starting to clean untracked files")

	cmd := exec.Command("git", "clean", "-fd")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Git clean failed: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("git clean failed: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully cleaned untracked files")
	log.Info("📋 Completed successfully - cleaned untracked files")
	return nil
}

func (g *GitClient) AddAll() error {
	log.Info("📋 Starting to add all changes")

	cmd := exec.Command("git", "add", ".")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Git add failed: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("git add failed: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully added all changes")
	log.Info("📋 Completed successfully - added all changes")
	return nil
}

func (g *GitClient) Commit(message string) error {
	log.Info("📋 Starting to commit with message: %s", message)

	cmd := exec.Command("git", "commit", "-m", message)
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Git commit failed with message '%s': %v\nOutput: %s", message, err, string(output))
		return fmt.Errorf("git commit failed: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully committed changes")
	log.Info("📋 Completed successfully - committed changes")
	return nil
}

func (g *GitClient) PushBranch(branchName string) error {
	log.Info("📋 Starting to push branch: %s", branchName)

	cmd := exec.Command("git", "push", "-u", "origin", branchName)
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Git push failed for branch %s: %v\nOutput: %s", branchName, err, string(output))
		return fmt.Errorf("git push failed: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully pushed branch: %s", branchName)
	log.Info("📋 Completed successfully - pushed branch")
	return nil
}

func (g *GitClient) CreatePullRequest(title, body, baseBranch string) (string, error) {
	log.Info("📋 Starting to create pull request: %s", title)

	// Validate and truncate PR title if necessary
	validationResult := ValidateAndTruncatePRTitle(title, body)

	// If title was truncated, prepend overflow to description
	finalBody := body
	if validationResult.DescriptionPrefix != "" {
		log.Warn("⚠️ PR title exceeded %d characters, truncating to '%s' and moving overflow to description",
			MaxGitHubPRTitleLength, validationResult.Title)
		finalBody = validationResult.DescriptionPrefix + body
	}

	cmd := exec.Command("gh", "pr", "create", "--title", validationResult.Title, "--body", finalBody, "--base", baseBranch)
	g.setWorkDir(cmd)
	output, err := g.executeWithRetry(cmd, "create pull request")

	if err != nil {
		log.Error("❌ GitHub PR creation failed for title '%s': %v\nOutput: %s", validationResult.Title, err, string(output))
		return "", fmt.Errorf("github pr creation failed: %w\nOutput: %s", err, string(output))
	}

	// The output contains the PR URL
	prURL := strings.TrimSpace(string(output))

	log.Info("✅ Successfully created pull request: %s", validationResult.Title)
	log.Info("📋 Completed successfully - created pull request")
	return prURL, nil
}

func (g *GitClient) GetPRURL(branchName string) (string, error) {
	log.Info("📋 Starting to get PR URL for branch: %s", branchName)

	cmd := exec.Command("gh", "pr", "view", branchName, "--json", "url", "--jq", ".url")
	g.setWorkDir(cmd)
	output, err := g.executeWithRetry(cmd, "get PR URL")

	if err != nil {
		log.Error("❌ Failed to get PR URL for branch %s: %v\nOutput: %s", branchName, err, string(output))
		return "", fmt.Errorf("failed to get PR URL: %w\nOutput: %s", err, string(output))
	}

	prURL := strings.TrimSpace(string(output))

	log.Info("✅ Successfully got PR URL: %s", prURL)
	log.Info("📋 Completed successfully - got PR URL")
	return prURL, nil
}

func (g *GitClient) GetCurrentBranch() (string, error) {
	log.Info("📋 Starting to get current branch")

	cmd := exec.Command("git", "branch", "--show-current")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to get current branch: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("failed to get current branch: %w\nOutput: %s", err, string(output))
	}

	branch := strings.TrimSpace(string(output))

	// When in detached HEAD state, git branch --show-current returns an empty string
	// without an error. We need to detect this and return an error since many operations
	// (like git push) require a valid branch name.
	if branch == "" {
		log.Error("❌ Repository is in detached HEAD state (no branch checked out)")
		return "", fmt.Errorf("repository is in detached HEAD state: no branch is currently checked out. This can happen after checking out a specific commit. Please checkout a branch first")
	}

	log.Info("✅ Current branch: %s", branch)
	log.Info("📋 Completed successfully - got current branch")
	return branch, nil
}

func (g *GitClient) GetDefaultBranch() (string, error) {
	log.Info("📋 Starting to determine default branch")

	// Run git remote show origin to get HEAD branch information
	cmd := exec.Command("git", "remote", "show", "origin")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("❌ Failed to run git remote show origin: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("failed to get remote information: %w\nOutput: %s", err, string(output))
	}

	// Parse the output to find the HEAD branch line
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "HEAD branch:") {
			// Extract branch name after "HEAD branch: "
			parts := strings.SplitN(trimmedLine, ":", 2)
			if len(parts) != 2 {
				log.Error("❌ Unexpected format in remote show output: %s", trimmedLine)
				return "", fmt.Errorf("unexpected format in remote show output: %s", trimmedLine)
			}

			branchName := strings.TrimSpace(parts[1])
			log.Info("✅ Default branch from remote: %s", branchName)
			log.Info("📋 Completed successfully - got default branch from remote")
			return branchName, nil
		}
	}

	log.Error("❌ Could not find HEAD branch in remote show output")
	return "", fmt.Errorf("could not determine default branch from remote show output")
}

func (g *GitClient) CreateAndCheckoutBranch(branchName string) error {
	log.Info("📋 Starting to create and checkout branch: %s", branchName)

	cmd := exec.Command("git", "checkout", "-b", branchName)
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Git checkout -b failed for branch %s: %v\nOutput: %s", branchName, err, string(output))
		return fmt.Errorf("git checkout -b failed: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully created and checked out branch: %s", branchName)
	log.Info("📋 Completed successfully - created and checked out branch")
	return nil
}

func (g *GitClient) IsGitRepository() error {
	log.Info("📋 Starting to check if current directory is a Git repository")

	cmd := exec.Command("git", "rev-parse", "--git-dir")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Not a Git repository: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("not a git repository: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Current directory is a Git repository")
	log.Info("📋 Completed successfully - validated Git repository")
	return nil
}

func (g *GitClient) IsGitRepositoryRoot() error {
	log.Info("📋 Starting to check if target directory is the Git repository root")

	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to get Git repository root: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("failed to get git repository root: %w\nOutput: %s", err, string(output))
	}

	gitRoot := strings.TrimSpace(string(output))

	// Get the target directory to compare against git root
	// If repo path is configured, use that; otherwise use current working directory
	targetDir := g.getRepoPath()
	if targetDir == "" {
		var err error
		targetDir, err = os.Getwd()
		if err != nil {
			log.Error("❌ Failed to get current working directory: %v", err)
			return fmt.Errorf("failed to get current working directory: %w", err)
		}
	}

	if gitRoot != targetDir {
		log.Error("❌ Not at Git repository root. Target: %s, Git root: %s", targetDir, gitRoot)
		return fmt.Errorf(
			"eksecd must be run from the Git repository root directory. Target: %s, Git root: %s",
			targetDir,
			gitRoot,
		)
	}

	log.Info("✅ Target directory is the Git repository root")
	log.Info("📋 Completed successfully - validated Git repository root")
	return nil
}

func (g *GitClient) HasRemoteRepository() error {
	log.Info("📋 Starting to check for remote repository")

	cmd := exec.Command("git", "remote", "-v")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to check remotes: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("failed to check git remotes: %w\nOutput: %s", err, string(output))
	}

	remotes := strings.TrimSpace(string(output))
	if remotes == "" {
		log.Error("❌ No remote repositories configured")
		return fmt.Errorf("no remote repositories configured")
	}

	log.Info("✅ Remote repository found")
	log.Info("📋 Completed successfully - validated remote repository")
	return nil
}

func (g *GitClient) IsGitHubCLIAvailable() error {
	log.Info("📋 Starting to check GitHub CLI availability")

	// Check if gh command exists
	cmd := exec.Command("gh", "--version")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ GitHub CLI not found: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("github cli (gh) not found: %w\nOutput: %s", err, string(output))
	}

	// Check if gh is authenticated
	cmd = exec.Command("gh", "auth", "status")
	g.setWorkDir(cmd)
	output, err = cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ GitHub CLI not authenticated: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("github cli not authenticated (run 'gh auth login'): %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ GitHub CLI is available and authenticated")
	log.Info("📋 Completed successfully - validated GitHub CLI")
	return nil
}

func (g *GitClient) HasUncommittedChanges() (bool, error) {
	log.Info("📋 Starting to check for uncommitted changes")

	// Check for staged and unstaged changes
	cmd := exec.Command("git", "status", "--porcelain")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to check git status: %v\nOutput: %s", err, string(output))
		return false, fmt.Errorf("failed to check git status: %w\nOutput: %s", err, string(output))
	}

	statusOutput := strings.TrimSpace(string(output))
	hasChanges := statusOutput != ""

	if hasChanges {
		log.Info("✅ Found uncommitted changes")
		log.Info("📄 Git status output: %s", statusOutput)
	} else {
		log.Info("✅ No uncommitted changes found")
	}

	log.Info("📋 Completed successfully - checked for uncommitted changes")
	return hasChanges, nil
}

func (g *GitClient) HasExistingPR(branchName string) (bool, error) {
	log.Info("📋 Starting to check for existing PR for branch: %s", branchName)

	// Use GitHub CLI to list PRs for the current branch
	cmd := exec.Command("gh", "pr", "list", "--head", branchName, "--json", "number")
	g.setWorkDir(cmd)
	output, err := g.executeWithRetry(cmd, "check existing PR")

	if err != nil {
		log.Error("❌ Failed to check for existing PR for branch %s: %v\nOutput: %s", branchName, err, string(output))
		return false, fmt.Errorf("failed to check for existing PR: %w\nOutput: %s", err, string(output))
	}

	// If output is "[]" (empty JSON array), no PRs exist for this branch
	outputStr := strings.TrimSpace(string(output))
	hasPR := outputStr != "[]" && outputStr != ""

	if hasPR {
		log.Info("✅ Found existing PR for branch: %s", branchName)
	} else {
		log.Info("✅ No existing PR found for branch: %s", branchName)
	}

	log.Info("📋 Completed successfully - checked for existing PR")
	return hasPR, nil
}

func (g *GitClient) GetLatestCommitHash() (string, error) {
	log.Info("📋 Starting to get latest commit hash")

	cmd := exec.Command("git", "rev-parse", "HEAD")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to get latest commit hash: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("failed to get latest commit hash: %w\nOutput: %s", err, string(output))
	}

	commitHash := strings.TrimSpace(string(output))
	log.Info("✅ Latest commit hash: %s", commitHash)
	log.Info("📋 Completed successfully - got latest commit hash")
	return commitHash, nil
}

// getRawRemoteURL gets the remote URL without any conversion (for error messages)
func (g *GitClient) getRawRemoteURL() (string, error) {
	log.Info("📋 Starting to get raw remote URL")

	cmd := exec.Command("git", "remote", "get-url", "origin")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to get raw remote URL: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("failed to get raw remote URL: %w\nOutput: %s", err, string(output))
	}

	rawRemoteURL := strings.TrimSpace(string(output))
	log.Info("✅ Raw remote URL: %s", rawRemoteURL)
	log.Info("📋 Completed successfully - got raw remote URL")
	return rawRemoteURL, nil
}

func (g *GitClient) GetRemoteURL() (string, error) {
	log.Info("📋 Starting to get remote URL")

	cmd := exec.Command("git", "remote", "get-url", "origin")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to get remote URL: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("failed to get remote URL: %w\nOutput: %s", err, string(output))
	}

	remoteURL := strings.TrimSpace(string(output))

	// Convert SSH URL to HTTPS if needed for GitHub links
	if strings.HasPrefix(remoteURL, "git@github.com:") {
		// Convert git@github.com:owner/repo.git to https://github.com/owner/repo
		remoteURL = strings.Replace(remoteURL, "git@github.com:", "https://github.com/", 1)
		remoteURL = strings.TrimSuffix(remoteURL, ".git")
	} else if strings.HasSuffix(remoteURL, ".git") {
		// Remove .git suffix from HTTPS URLs
		remoteURL = strings.TrimSuffix(remoteURL, ".git")
	}

	log.Info("✅ Remote URL: %s", remoteURL)
	log.Info("📋 Completed successfully - got remote URL")
	return remoteURL, nil
}

func (g *GitClient) GetRepositoryIdentifier() (string, error) {
	log.Info("📋 Starting to get repository identifier")

	remoteURL, err := g.GetRemoteURL()
	if err != nil {
		log.Error("❌ Failed to get remote URL: %v", err)
		return "", fmt.Errorf("failed to get remote URL: %w", err)
	}

	// Extract the repository identifier from the URL (e.g., "github.com/owner/repo")
	if !strings.HasPrefix(remoteURL, "https://") {
		log.Error("❌ Unsupported remote URL format: %s", remoteURL)
		return "", fmt.Errorf("unsupported remote URL format: %s", remoteURL)
	}

	// Remove https:// prefix
	repoIdentifier := strings.TrimPrefix(remoteURL, "https://")

	// Strip x-access-token authentication if present (e.g., "x-access-token:ghs_...@github.com/owner/repo")
	if strings.Contains(repoIdentifier, "@") {
		parts := strings.Split(repoIdentifier, "@")
		if len(parts) >= 2 {
			// Take everything after the last @ symbol (handles multiple @ symbols)
			repoIdentifier = parts[len(parts)-1]
		}
	}

	log.Info("✅ Repository identifier: %s", repoIdentifier)
	log.Info("📋 Completed successfully - got repository identifier")
	return repoIdentifier, nil
}

func (g *GitClient) GetPRDescription(branchName string) (string, error) {
	log.Info("📋 Starting to get PR description for branch: %s", branchName)

	cmd := exec.Command("gh", "pr", "view", branchName, "--json", "body", "--jq", ".body")
	g.setWorkDir(cmd)
	output, err := g.executeWithRetry(cmd, "get PR description")

	if err != nil {
		log.Error("❌ Failed to get PR description for branch %s: %v\nOutput: %s", branchName, err, string(output))
		return "", fmt.Errorf("failed to get PR description: %w\nOutput: %s", err, string(output))
	}

	description := strings.TrimSpace(string(output))
	log.Info("✅ Successfully got PR description")
	log.Info("📋 Completed successfully - got PR description")
	return description, nil
}

func (g *GitClient) UpdatePRDescription(branchName, newDescription string) error {
	log.Info("📋 Starting to update PR description for branch: %s", branchName)

	cmd := exec.Command("gh", "pr", "edit", branchName, "--body", newDescription)
	g.setWorkDir(cmd)
	output, err := g.executeWithRetry(cmd, "update PR description")

	if err != nil {
		log.Error("❌ Failed to update PR description for branch %s: %v\nOutput: %s", branchName, err, string(output))
		return fmt.Errorf("failed to update PR description: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully updated PR description")
	log.Info("📋 Completed successfully - updated PR description")
	return nil
}

func (g *GitClient) GetPRTitle(branchName string) (string, error) {
	log.Info("📋 Starting to get PR title for branch: %s", branchName)

	cmd := exec.Command("gh", "pr", "view", branchName, "--json", "title", "--jq", ".title")
	g.setWorkDir(cmd)
	output, err := g.executeWithRetry(cmd, "get PR title")

	if err != nil {
		log.Error("❌ Failed to get PR title for branch %s: %v\nOutput: %s", branchName, err, string(output))
		return "", fmt.Errorf("failed to get PR title: %w\nOutput: %s", err, string(output))
	}

	title := strings.TrimSpace(string(output))
	log.Info("✅ Successfully got PR title: %s", title)
	log.Info("📋 Completed successfully - got PR title")
	return title, nil
}

func (g *GitClient) UpdatePRTitle(branchName, newTitle string) error {
	log.Info("📋 Starting to update PR title for branch: %s", branchName)

	// Get current description first in case we need to prepend overflow
	currentDescription, err := g.GetPRDescription(branchName)
	if err != nil {
		log.Warn("⚠️ Failed to get current PR description, continuing with title update only: %v", err)
		currentDescription = ""
	}

	// Validate and truncate PR title if necessary
	validationResult := ValidateAndTruncatePRTitle(newTitle, currentDescription)

	// If title was truncated, prepend overflow to description
	if validationResult.DescriptionPrefix != "" {
		log.Warn("⚠️ PR title exceeded %d characters, truncating to '%s' and moving overflow to description",
			MaxGitHubPRTitleLength, validationResult.Title)

		newDescription := validationResult.DescriptionPrefix + currentDescription

		// Update both title and description
		if err := g.UpdatePRDescription(branchName, newDescription); err != nil {
			log.Error("❌ Failed to update PR description with overflow text: %v", err)
			return fmt.Errorf("failed to update PR description with overflow: %w", err)
		}
	}

	// Update the title
	cmd := exec.Command("gh", "pr", "edit", branchName, "--title", validationResult.Title)
	g.setWorkDir(cmd)
	output, err := g.executeWithRetry(cmd, "update PR title")

	if err != nil {
		log.Error("❌ Failed to update PR title for branch %s: %v\nOutput: %s", branchName, err, string(output))
		return fmt.Errorf("failed to update PR title: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully updated PR title")
	log.Info("📋 Completed successfully - updated PR title")
	return nil
}

func (g *GitClient) GetPRState(branchName string) (string, error) {
	log.Info("📋 Starting to get PR state for branch: %s", branchName)

	cmd := exec.Command("gh", "pr", "view", branchName, "--json", "state", "--jq", ".state")
	g.setWorkDir(cmd)
	output, err := g.executeWithRetry(cmd, "get PR state")

	if err != nil {
		log.Error("❌ Failed to get PR state for branch %s: %v\nOutput: %s", branchName, err, string(output))
		return "", fmt.Errorf("failed to get PR state: %w\nOutput: %s", err, string(output))
	}

	state := strings.TrimSpace(string(output))
	log.Info("✅ Retrieved PR state: %s", state)
	log.Info("📋 Completed successfully - got PR state")
	return strings.ToLower(state), nil
}

func (g *GitClient) ExtractPRIDFromURL(prURL string) string {
	if prURL == "" {
		return ""
	}

	// Extract PR number from URL like https://github.com/user/repo/pull/1234
	parts := strings.Split(prURL, "/")
	if len(parts) > 0 && parts[len(parts)-1] != "" {
		return parts[len(parts)-1]
	}

	return ""
}

func (g *GitClient) GetPRStateByID(prID string) (string, error) {
	log.Info("📋 Starting to get PR state by ID: %s", prID)

	cmd := exec.Command("gh", "pr", "view", prID, "--json", "state", "--jq", ".state")
	g.setWorkDir(cmd)
	output, err := g.executeWithRetry(cmd, "get PR state by ID")

	if err != nil {
		log.Error("❌ Failed to get PR state for PR ID %s: %v\nOutput: %s", prID, err, string(output))
		return "", fmt.Errorf("failed to get PR state by ID: %w\nOutput: %s", err, string(output))
	}

	state := strings.TrimSpace(string(output))
	log.Info("✅ Retrieved PR state by ID: %s", state)
	log.Info("📋 Completed successfully - got PR state by ID")
	return strings.ToLower(state), nil
}

func (g *GitClient) GetLocalBranches() ([]string, error) {
	log.Info("📋 Starting to get local branches")

	cmd := exec.Command("git", "branch", "--format=%(refname:short)")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to get local branches: %v\nOutput: %s", err, string(output))
		return nil, fmt.Errorf("failed to get local branches: %w\nOutput: %s", err, string(output))
	}

	branchList := strings.TrimSpace(string(output))
	if branchList == "" {
		log.Info("✅ No local branches found")
		log.Info("📋 Completed successfully - got local branches")
		return []string{}, nil
	}

	branches := strings.Split(branchList, "\n")
	var cleanBranches []string
	for _, branch := range branches {
		cleanBranch := strings.TrimSpace(branch)
		if cleanBranch != "" {
			cleanBranches = append(cleanBranches, cleanBranch)
		}
	}

	log.Info("✅ Found %d local branches", len(cleanBranches))
	log.Info("📋 Completed successfully - got local branches")
	return cleanBranches, nil
}

func (g *GitClient) RemoteBranchExists(branchName string) (bool, error) {
	log.Info("📋 Starting to check if remote branch exists: %s", branchName)

	// Use git ls-remote to check if the branch exists on the remote
	// This checks origin/branchName without fetching
	cmd := exec.Command("git", "ls-remote", "--heads", "origin", branchName)
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to check remote branch %s: %v\nOutput: %s", branchName, err, string(output))
		return false, fmt.Errorf("failed to check remote branch %s: %w\nOutput: %s", branchName, err, string(output))
	}

	// If output is empty, the branch doesn't exist on remote
	exists := strings.TrimSpace(string(output)) != ""

	if exists {
		log.Info("✅ Remote branch %s exists", branchName)
	} else {
		log.Info("ℹ️ Remote branch %s does not exist", branchName)
	}

	log.Info("📋 Completed successfully - checked remote branch existence")
	return exists, nil
}

func (g *GitClient) CheckoutRemoteBranch(branchName string) error {
	log.Info("📋 Starting to checkout remote branch: %s", branchName)

	// First, fetch the branch from remote
	fetchCmd := exec.Command("git", "fetch", "origin", branchName)
	g.setWorkDir(fetchCmd)
	fetchOutput, fetchErr := fetchCmd.CombinedOutput()

	if fetchErr != nil {
		log.Error("❌ Git fetch failed for branch %s: %v\nOutput: %s", branchName, fetchErr, string(fetchOutput))
		return fmt.Errorf("git fetch failed: %w\nOutput: %s", fetchErr, string(fetchOutput))
	}

	log.Info("✅ Successfully fetched remote branch: %s", branchName)

	// Now checkout the branch, creating a local tracking branch
	checkoutCmd := exec.Command("git", "checkout", "-b", branchName, fmt.Sprintf("origin/%s", branchName))
	g.setWorkDir(checkoutCmd)
	checkoutOutput, checkoutErr := checkoutCmd.CombinedOutput()

	if checkoutErr != nil {
		log.Error("❌ Git checkout failed for remote branch %s: %v\nOutput: %s", branchName, checkoutErr, string(checkoutOutput))
		return fmt.Errorf("git checkout failed: %w\nOutput: %s", checkoutErr, string(checkoutOutput))
	}

	log.Info("✅ Successfully checked out remote branch: %s", branchName)
	log.Info("📋 Completed successfully - checked out remote branch")
	return nil
}

func (g *GitClient) DeleteLocalBranch(branchName string) error {
	log.Info("📋 Starting to delete local branch: %s", branchName)

	cmd := exec.Command("git", "branch", "-D", branchName)
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to delete local branch %s: %v\nOutput: %s", branchName, err, string(output))
		return fmt.Errorf("failed to delete local branch %s: %w\nOutput: %s", branchName, err, string(output))
	}

	log.Info("✅ Successfully deleted local branch: %s", branchName)
	log.Info("📋 Completed successfully - deleted local branch")
	return nil
}

func (g *GitClient) ValidateRemoteAccess() error {
	log.Info("📋 Starting to validate remote repository access")

	// Get raw remote URL for error messages (without conversion)
	rawRemoteURL, err := g.getRawRemoteURL()
	if err != nil {
		log.Error("❌ Failed to get remote URL: %v", err)
		return fmt.Errorf("failed to get remote URL: %w", err)
	}

	log.Info("🔍 Testing remote access for: %s", rawRemoteURL)

	// Test remote access with git ls-remote HEAD with 10s timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "ls-remote", "origin", "HEAD")
	g.setWorkDir(cmd)

	// Set environment variables to prevent credential prompting
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"SSH_ASKPASS=",
		"DISPLAY=", // Disable X11 forwarding for SSH
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=10", // Force non-interactive SSH
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("❌ Remote access validation failed: %v\nOutput: %s", err, string(output))
		return g.parseRemoteAccessError(err, string(output), rawRemoteURL)
	}

	log.Info("✅ Remote repository access validated successfully")
	log.Info("📋 Completed successfully - validated remote repository access")
	return nil
}

func (g *GitClient) parseRemoteAccessError(err error, output, remoteURL string) error {
	outputStr := strings.ToLower(output)

	// Check for timeout first
	if strings.Contains(err.Error(), "context deadline exceeded") {
		return fmt.Errorf(
			"remote repository access timed out after 10 seconds for %s. Check your network connection",
			remoteURL,
		)
	}

	// SSH credential issues
	if strings.Contains(outputStr, "permission denied (publickey)") {
		return fmt.Errorf(
			"SSH key authentication failed for %s. Please ensure your SSH key is added to your Git provider and loaded in ssh-agent (or use a key without passphrase)",
			remoteURL,
		)
	}

	// SSH passphrase/key loading issues
	if strings.Contains(outputStr, "enter passphrase") || strings.Contains(outputStr, "bad passphrase") {
		return fmt.Errorf(
			"SSH key requires passphrase for %s. Please add your key to ssh-agent with 'ssh-add ~/.ssh/id_rsa' or use a key without passphrase",
			remoteURL,
		)
	}

	if strings.Contains(outputStr, "could not read from remote repository") {
		return fmt.Errorf("cannot access remote repository %s. Check your SSH keys or network connection", remoteURL)
	}

	if strings.Contains(outputStr, "host key verification failed") {
		return fmt.Errorf(
			"SSH host key verification failed for %s. Run 'ssh-keyscan' to add the host key or disable StrictHostKeyChecking",
			remoteURL,
		)
	}

	// HTTPS credential issues
	if strings.Contains(outputStr, "authentication failed") {
		return fmt.Errorf("HTTPS authentication failed for %s. Please check credentials or use SSH", remoteURL)
	}

	if strings.Contains(outputStr, "repository not found") {
		return fmt.Errorf("repository not found: %s. Check the URL and your access permissions", remoteURL)
	}

	// Network/connectivity issues
	if strings.Contains(outputStr, "could not resolve host") {
		return fmt.Errorf("network error: cannot resolve host for %s", remoteURL)
	}

	if strings.Contains(outputStr, "connection timed out") || strings.Contains(outputStr, "network is unreachable") {
		return fmt.Errorf("network connection failed for %s. Check your internet connection", remoteURL)
	}

	// Generic fallback
	return fmt.Errorf("remote repository access failed for %s: %w\nOutput: %s", remoteURL, err, output)
}

type RemoteRepoDetails struct {
	Owner string
	Repo  string
}

func (g *GitClient) extractRemoteRepoDetails(remoteURL string) (*RemoteRepoDetails, error) {
	if strings.HasPrefix(remoteURL, "git@github.com:") {
		// SSH format: git@github.com:owner/repo.git
		parts := strings.TrimPrefix(remoteURL, "git@github.com:")
		parts = strings.TrimSuffix(parts, ".git")
		pathParts := strings.Split(parts, "/")
		if len(pathParts) != 2 {
			return nil, fmt.Errorf("invalid SSH URL format: %s", remoteURL)
		}
		return &RemoteRepoDetails{
			Owner: pathParts[0],
			Repo:  pathParts[1],
		}, nil
	} else if strings.Contains(remoteURL, "github.com") {
		// HTTPS format: https://github.com/owner/repo.git or https://x-access-token:TOKEN@github.com/owner/repo.git
		// Extract the path part after github.com
		var pathPart string
		if strings.Contains(remoteURL, "@github.com") {
			// Already has token format
			parts := strings.Split(remoteURL, "@github.com")
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid HTTPS URL format: %s", remoteURL)
			}
			pathPart = parts[1]
		} else {
			// Standard HTTPS format
			parts := strings.Split(remoteURL, "github.com")
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid HTTPS URL format: %s", remoteURL)
			}
			pathPart = parts[1]
		}

		// Remove leading slash and .git suffix
		pathPart = strings.TrimPrefix(pathPart, "/")
		pathPart = strings.TrimSuffix(pathPart, ".git")

		pathParts := strings.Split(pathPart, "/")
		if len(pathParts) != 2 {
			return nil, fmt.Errorf("invalid HTTPS URL path: %s", pathPart)
		}
		return &RemoteRepoDetails{
			Owner: pathParts[0],
			Repo:  pathParts[1],
		}, nil
	}

	// Not a GitHub repository
	return nil, nil
}

func (g *GitClient) UpdateRemoteURLWithToken(token string) error {
	log.Info("📋 Starting to update remote URL with GitHub token")

	// Get current remote URL
	cmd := exec.Command("git", "remote", "get-url", "origin")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("❌ Failed to get current remote URL: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("failed to get current remote URL: %w\nOutput: %s", err, string(output))
	}

	currentURL := strings.TrimSpace(string(output))
	log.Info("🔍 Current remote URL: %s", currentURL)

	// Extract repository details
	repoDetails, err := g.extractRemoteRepoDetails(currentURL)
	if err != nil {
		log.Error("❌ Failed to parse remote URL: %v", err)
		return fmt.Errorf("failed to parse remote URL: %w", err)
	}

	if repoDetails == nil {
		log.Info("⚠️ Not a GitHub repository, skipping token update: %s", currentURL)
		return nil
	}

	// Construct new URL with token
	newURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, repoDetails.Owner, repoDetails.Repo)

	// Update the remote URL
	cmd = exec.Command("git", "remote", "set-url", "origin", newURL)
	g.setWorkDir(cmd)
	output, err = cmd.CombinedOutput()
	if err != nil {
		log.Error("❌ Failed to update remote URL: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("failed to update remote URL: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully updated remote URL with GitHub token for %s/%s", repoDetails.Owner, repoDetails.Repo)
	log.Info("📋 Completed successfully - updated remote URL with token")
	return nil
}

// FetchOrigin fetches updates from the origin remote.
// This is safe for concurrent calls as it only updates remote tracking refs.
func (g *GitClient) FetchOrigin() error {
	log.Info("📋 Starting to fetch from origin")
	cmd := exec.Command("git", "fetch", "origin")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("❌ Git fetch failed: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("git fetch failed: %w\nOutput: %s", err, string(output))
	}
	log.Info("✅ Successfully fetched from origin")
	return nil
}

// FindPRTemplate searches for GitHub PR template in standard locations
// Returns the template content if found, empty string otherwise
func (g *GitClient) FindPRTemplate() (string, error) {
	log.Info("🔍 Searching for GitHub PR template")

	// Standard locations per GitHub docs
	// https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/creating-a-pull-request-template-for-your-repository
	templatePaths := []string{
		"pull_request_template.md",
		"PULL_REQUEST_TEMPLATE.md",
		".github/pull_request_template.md",
		".github/PULL_REQUEST_TEMPLATE.md",
		"docs/pull_request_template.md",
		"docs/PULL_REQUEST_TEMPLATE.md",
		".github/PULL_REQUEST_TEMPLATE/pull_request_template.md",
	}

	for _, path := range templatePaths {
		if content, err := os.ReadFile(path); err == nil {
			log.Info("✅ Found PR template at: %s", path)
			return strings.TrimSpace(string(content)), nil
		}
	}

	log.Info("ℹ️ No PR template found in standard locations")
	return "", nil
}

// =============================================================================
// Worktree Operations
// =============================================================================

// CreateWorktree creates a new worktree at the specified path for the given branch.
// If baseRef is provided (e.g., "origin/main"), the branch is created from that ref.
// If baseRef is empty, the branch is created from the current HEAD.
func (g *GitClient) CreateWorktree(worktreePath, branchName, baseRef string) error {
	log.Info("📋 Starting to create worktree at %s for branch %s (baseRef: %s)", worktreePath, branchName, baseRef)

	// git worktree add <path> -b <branch> [<baseRef>] creates worktree with a new branch
	var cmd *exec.Cmd
	if baseRef != "" {
		cmd = exec.Command("git", "worktree", "add", worktreePath, "-b", branchName, baseRef)
	} else {
		cmd = exec.Command("git", "worktree", "add", worktreePath, "-b", branchName)
	}
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to create worktree: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("failed to create worktree: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully created worktree at %s for branch %s", worktreePath, branchName)
	return nil
}

// RemoveWorktree removes a worktree at the specified path.
// The --force flag is used to remove even if there are uncommitted changes.
func (g *GitClient) RemoveWorktree(worktreePath string) error {
	log.Info("📋 Starting to remove worktree at %s", worktreePath)

	cmd := exec.Command("git", "worktree", "remove", worktreePath, "--force")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to remove worktree: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("failed to remove worktree: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully removed worktree at %s", worktreePath)
	return nil
}

// ListWorktrees returns a list of all worktrees for the repository
func (g *GitClient) ListWorktrees() ([]WorktreeInfo, error) {
	log.Info("📋 Starting to list worktrees")

	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to list worktrees: %v\nOutput: %s", err, string(output))
		return nil, fmt.Errorf("failed to list worktrees: %w\nOutput: %s", err, string(output))
	}

	// Parse porcelain output format:
	// worktree /path/to/worktree
	// HEAD <commit>
	// branch refs/heads/<branch>
	// (empty line)
	var worktrees []WorktreeInfo
	var current WorktreeInfo

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if current.Path != "" {
				worktrees = append(worktrees, current)
				current = WorktreeInfo{}
			}
			continue
		}

		if strings.HasPrefix(line, "worktree ") {
			current.Path = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "HEAD ") {
			current.Commit = strings.TrimPrefix(line, "HEAD ")
		} else if strings.HasPrefix(line, "branch ") {
			// Convert refs/heads/branch to just branch
			ref := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		}
	}

	// Don't forget the last worktree if output doesn't end with empty line
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	log.Info("✅ Found %d worktrees", len(worktrees))
	return worktrees, nil
}

// WorktreeExists checks if a worktree exists at the given path
func (g *GitClient) WorktreeExists(worktreePath string) bool {
	worktrees, err := g.ListWorktrees()
	if err != nil {
		return false
	}

	// Resolve symlinks in the input path for accurate comparison.
	// On macOS, /var is a symlink to /private/var, so git stores paths
	// with resolved symlinks while callers may pass unresolved paths.
	normalizedInput, err := filepath.EvalSymlinks(worktreePath)
	if err != nil {
		// If path doesn't exist or can't be resolved, use original
		normalizedInput = worktreePath
	}

	for _, wt := range worktrees {
		if wt.Path == normalizedInput {
			return true
		}
	}

	return false
}

// PruneWorktrees removes administrative files for worktrees that no longer exist on disk
func (g *GitClient) PruneWorktrees() error {
	log.Info("📋 Starting to prune stale worktree entries")

	cmd := exec.Command("git", "worktree", "prune")
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to prune worktrees: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("failed to prune worktrees: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully pruned stale worktree entries")
	return nil
}

// =============================================================================
// Worktree-Specific Git Operations
// =============================================================================

// ResetHardInWorktree performs a git reset --hard HEAD in the specified worktree
func (g *GitClient) ResetHardInWorktree(worktreePath string) error {
	log.Info("📋 Starting to reset hard in worktree: %s", worktreePath)

	// Check if worktree has any commits first
	hasCommits, err := g.hasCommitsInDir(worktreePath)
	if err != nil {
		log.Error("❌ Failed to check if worktree has commits: %v", err)
		return fmt.Errorf("failed to check if worktree has commits: %w", err)
	}

	if !hasCommits {
		log.Info("ℹ️ Worktree has no commits yet - skipping reset")
		return nil
	}

	cmd := exec.Command("git", "reset", "--hard", "HEAD")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Git reset hard failed in worktree: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("git reset hard failed in worktree: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully reset hard in worktree: %s", worktreePath)
	return nil
}

// hasCommitsInDir checks if the repository at the given directory has any commits
func (g *GitClient) hasCommitsInDir(dir string) (bool, error) {
	cmd := exec.Command("git", "rev-parse", "--verify", "HEAD")
	cmd.Dir = dir
	err := cmd.Run()
	return err == nil, nil
}

// CleanUntrackedInWorktree removes untracked files in the specified worktree
func (g *GitClient) CleanUntrackedInWorktree(worktreePath string) error {
	log.Info("📋 Starting to clean untracked files in worktree: %s", worktreePath)

	cmd := exec.Command("git", "clean", "-fd")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Git clean failed in worktree: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("git clean failed in worktree: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully cleaned untracked files in worktree: %s", worktreePath)
	return nil
}

// PullLatestInWorktree pulls latest changes in the specified worktree
func (g *GitClient) PullLatestInWorktree(worktreePath string) error {
	log.Info("📋 Starting to pull latest changes in worktree: %s", worktreePath)

	cmd := exec.Command("git", "pull")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		outputStr := strings.ToLower(string(output))

		// Check if error is due to no upstream branch configured
		if strings.Contains(outputStr, "no tracking information") ||
			strings.Contains(outputStr, "no upstream branch") ||
			strings.Contains(outputStr, "there is no tracking information for the current branch") {
			log.Info("ℹ️ No remote branch exists yet in worktree - nothing to pull")
			return nil
		}

		// Check if error is due to remote branch being deleted
		if strings.Contains(outputStr, "no such ref was fetched") ||
			strings.Contains(outputStr, "couldn't find remote ref") {
			// Check if branch was ever pushed (has upstream tracking)
			hasPushed, trackErr := g.HasUpstreamTrackingInWorktree(worktreePath)
			if trackErr != nil {
				log.Warn("⚠️ Failed to check upstream tracking in worktree: %v", trackErr)
			}

			if !hasPushed {
				// Branch was never pushed - this is not an error, just nothing to pull
				log.Info("ℹ️ Branch in worktree has no upstream - never pushed, nothing to pull")
				return nil
			}

			// Branch was pushed but now deleted on remote
			log.Warn("⚠️ Remote branch has been deleted for worktree")
			return fmt.Errorf("remote branch deleted: %w", err)
		}

		log.Error("❌ Git pull failed in worktree: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("git pull failed in worktree: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully pulled latest changes in worktree: %s", worktreePath)
	return nil
}

// AddAllInWorktree adds all changes in the specified worktree
func (g *GitClient) AddAllInWorktree(worktreePath string) error {
	log.Info("📋 Starting to add all changes in worktree: %s", worktreePath)

	cmd := exec.Command("git", "add", ".")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Git add failed in worktree: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("git add failed in worktree: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully added all changes in worktree: %s", worktreePath)
	return nil
}

// CommitInWorktree commits changes in the specified worktree
func (g *GitClient) CommitInWorktree(worktreePath, message string) error {
	log.Info("📋 Starting to commit in worktree: %s", worktreePath)

	cmd := exec.Command("git", "commit", "-m", message)
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Git commit failed in worktree: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("git commit failed in worktree: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully committed changes in worktree: %s", worktreePath)
	return nil
}

// PushBranchFromWorktree pushes a branch from the specified worktree
func (g *GitClient) PushBranchFromWorktree(worktreePath, branchName string) error {
	log.Info("📋 Starting to push branch %s from worktree: %s", branchName, worktreePath)

	cmd := exec.Command("git", "push", "-u", "origin", branchName)
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Git push failed in worktree: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("git push failed in worktree: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully pushed branch %s from worktree: %s", branchName, worktreePath)
	return nil
}

// HasUncommittedChangesInWorktree checks if the specified worktree has uncommitted changes
func (g *GitClient) HasUncommittedChangesInWorktree(worktreePath string) (bool, error) {
	log.Info("📋 Starting to check for uncommitted changes in worktree: %s", worktreePath)

	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to check git status in worktree: %v\nOutput: %s", err, string(output))
		return false, fmt.Errorf("failed to check git status in worktree: %w\nOutput: %s", err, string(output))
	}

	statusOutput := strings.TrimSpace(string(output))
	hasChanges := statusOutput != ""

	if hasChanges {
		log.Info("✅ Found uncommitted changes in worktree")
	} else {
		log.Info("✅ No uncommitted changes found in worktree")
	}

	return hasChanges, nil
}

// GetCurrentBranchInWorktree gets the current branch name in the specified worktree
func (g *GitClient) GetCurrentBranchInWorktree(worktreePath string) (string, error) {
	log.Info("📋 Starting to get current branch in worktree: %s", worktreePath)

	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to get current branch in worktree: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("failed to get current branch in worktree: %w\nOutput: %s", err, string(output))
	}

	branch := strings.TrimSpace(string(output))
	if branch == "" {
		log.Error("❌ Worktree is in detached HEAD state")
		return "", fmt.Errorf("worktree is in detached HEAD state")
	}

	log.Info("✅ Current branch in worktree: %s", branch)
	return branch, nil
}

// GetLatestCommitHashInWorktree gets the latest commit hash in the specified worktree
func (g *GitClient) GetLatestCommitHashInWorktree(worktreePath string) (string, error) {
	log.Info("📋 Starting to get latest commit hash in worktree: %s", worktreePath)

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to get latest commit hash in worktree: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("failed to get latest commit hash in worktree: %w\nOutput: %s", err, string(output))
	}

	commitHash := strings.TrimSpace(string(output))
	log.Info("✅ Latest commit hash in worktree: %s", commitHash)
	return commitHash, nil
}

// GetRemoteURLInWorktree gets the remote URL from the specified worktree
func (g *GitClient) GetRemoteURLInWorktree(worktreePath string) (string, error) {
	log.Info("📋 Starting to get remote URL in worktree: %s", worktreePath)

	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to get remote URL in worktree: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("failed to get remote URL in worktree: %w\nOutput: %s", err, string(output))
	}

	remoteURL := strings.TrimSpace(string(output))

	// Convert SSH URL to HTTPS if needed for GitHub links
	if strings.HasPrefix(remoteURL, "git@github.com:") {
		remoteURL = strings.Replace(remoteURL, "git@github.com:", "https://github.com/", 1)
		remoteURL = strings.TrimSuffix(remoteURL, ".git")
	} else if strings.HasSuffix(remoteURL, ".git") {
		remoteURL = strings.TrimSuffix(remoteURL, ".git")
	}

	log.Info("✅ Remote URL in worktree: %s", remoteURL)
	return remoteURL, nil
}

// HasExistingPRInWorktree checks if a PR already exists for a branch from the worktree context
func (g *GitClient) HasExistingPRInWorktree(worktreePath, branchName string) (bool, error) {
	log.Info("📋 Starting to check for existing PR for branch %s in worktree: %s", branchName, worktreePath)

	cmd := exec.Command("gh", "pr", "list", "--head", branchName, "--json", "number")
	cmd.Dir = worktreePath
	output, err := g.executeWithRetryInDir(cmd, worktreePath, "check existing PR")

	if err != nil {
		log.Error("❌ Failed to check for existing PR: %v\nOutput: %s", err, string(output))
		return false, fmt.Errorf("failed to check for existing PR: %w\nOutput: %s", err, string(output))
	}

	outputStr := strings.TrimSpace(string(output))
	hasPR := outputStr != "[]" && outputStr != ""

	if hasPR {
		log.Info("✅ Found existing PR for branch: %s", branchName)
	} else {
		log.Info("✅ No existing PR found for branch: %s", branchName)
	}

	return hasPR, nil
}

// executeWithRetryInDir is like executeWithRetry but with explicit working directory.
// Rate limit errors use a longer backoff (up to 10 minutes) since GitHub rate limits reset on longer windows.
func (g *GitClient) executeWithRetryInDir(cmd *exec.Cmd, workDir, operationName string) ([]byte, error) {
	var output []byte
	var err error
	var rateLimitDetected bool

	retryBackoff := backoff.NewExponentialBackOff()
	retryBackoff.InitialInterval = 2 * time.Second
	retryBackoff.MaxInterval = 30 * time.Second
	retryBackoff.MaxElapsedTime = 2 * time.Minute
	retryBackoff.Multiplier = 2

	retryOperation := func() error {
		output, err = cmd.CombinedOutput()

		if err != nil && isRecoverableGHError(err, string(output)) {
			// On first rate limit detection, extend the backoff parameters
			if !rateLimitDetected && isRateLimitError(err, string(output)) {
				rateLimitDetected = true
				retryBackoff.MaxInterval = 60 * time.Second
				retryBackoff.MaxElapsedTime = 10 * time.Minute
				log.Info("⏳ GitHub API rate limit detected for %s, extending retry window to 10 minutes...", operationName)
			} else {
				log.Info("⏳ GitHub API recoverable error detected for %s, retrying...", operationName)
			}
			cmd = exec.Command(cmd.Args[0], cmd.Args[1:]...)
			cmd.Dir = workDir
			return err
		}

		return nil
	}

	retryErr := backoff.Retry(retryOperation, retryBackoff)
	if retryErr != nil {
		if err != nil {
			return output, err
		}
		return output, retryErr
	}

	return output, err
}

// CreatePullRequestInWorktree creates a pull request from the specified worktree context
func (g *GitClient) CreatePullRequestInWorktree(worktreePath, title, body, baseBranch string) (string, error) {
	log.Info("📋 Starting to create pull request from worktree: %s", worktreePath)

	// Validate and truncate PR title if necessary
	validationResult := ValidateAndTruncatePRTitle(title, body)

	finalBody := body
	if validationResult.DescriptionPrefix != "" {
		log.Warn("⚠️ PR title exceeded %d characters, truncating", MaxGitHubPRTitleLength)
		finalBody = validationResult.DescriptionPrefix + body
	}

	cmd := exec.Command("gh", "pr", "create", "--title", validationResult.Title, "--body", finalBody, "--base", baseBranch)
	cmd.Dir = worktreePath
	output, err := g.executeWithRetryInDir(cmd, worktreePath, "create pull request")

	if err != nil {
		log.Error("❌ GitHub PR creation failed: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("github pr creation failed: %w\nOutput: %s", err, string(output))
	}

	prURL := strings.TrimSpace(string(output))
	log.Info("✅ Successfully created pull request: %s", prURL)
	return prURL, nil
}

// GetPRURLInWorktree gets the PR URL for a branch from the worktree context
func (g *GitClient) GetPRURLInWorktree(worktreePath, branchName string) (string, error) {
	log.Info("📋 Starting to get PR URL for branch %s from worktree: %s", branchName, worktreePath)

	cmd := exec.Command("gh", "pr", "view", branchName, "--json", "url", "--jq", ".url")
	cmd.Dir = worktreePath
	output, err := g.executeWithRetryInDir(cmd, worktreePath, "get PR URL")

	if err != nil {
		log.Error("❌ Failed to get PR URL: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("failed to get PR URL: %w\nOutput: %s", err, string(output))
	}

	prURL := strings.TrimSpace(string(output))
	log.Info("✅ PR URL: %s", prURL)
	return prURL, nil
}

// GetDefaultBranchInWorktree gets the default branch from the worktree context
func (g *GitClient) GetDefaultBranchInWorktree(worktreePath string) (string, error) {
	log.Info("📋 Starting to determine default branch from worktree: %s", worktreePath)

	cmd := exec.Command("git", "remote", "show", "origin")
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to run git remote show origin: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("failed to get remote information: %w\nOutput: %s", err, string(output))
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "HEAD branch:") {
			parts := strings.SplitN(trimmedLine, ":", 2)
			if len(parts) != 2 {
				return "", fmt.Errorf("unexpected format in remote show output: %s", trimmedLine)
			}

			branchName := strings.TrimSpace(parts[1])
			log.Info("✅ Default branch from worktree: %s", branchName)
			return branchName, nil
		}
	}

	return "", fmt.Errorf("could not determine default branch from remote show output")
}

// GetPRTitleInWorktree gets the PR title for a branch from the worktree context
func (g *GitClient) GetPRTitleInWorktree(worktreePath, branchName string) (string, error) {
	log.Info("📋 Starting to get PR title for branch %s from worktree: %s", branchName, worktreePath)

	cmd := exec.Command("gh", "pr", "view", branchName, "--json", "title", "--jq", ".title")
	cmd.Dir = worktreePath
	output, err := g.executeWithRetryInDir(cmd, worktreePath, "get PR title")

	if err != nil {
		log.Error("❌ Failed to get PR title: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("failed to get PR title: %w\nOutput: %s", err, string(output))
	}

	title := strings.TrimSpace(string(output))
	log.Info("✅ PR title: %s", title)
	return title, nil
}

// GetPRDescriptionInWorktree gets the PR description for a branch from the worktree context
func (g *GitClient) GetPRDescriptionInWorktree(worktreePath, branchName string) (string, error) {
	log.Info("📋 Starting to get PR description for branch %s from worktree: %s", branchName, worktreePath)

	cmd := exec.Command("gh", "pr", "view", branchName, "--json", "body", "--jq", ".body")
	cmd.Dir = worktreePath
	output, err := g.executeWithRetryInDir(cmd, worktreePath, "get PR description")

	if err != nil {
		log.Error("❌ Failed to get PR description: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("failed to get PR description: %w\nOutput: %s", err, string(output))
	}

	description := strings.TrimSpace(string(output))
	log.Info("✅ Got PR description")
	return description, nil
}

// UpdatePRTitleInWorktree updates the PR title for a branch from the worktree context
func (g *GitClient) UpdatePRTitleInWorktree(worktreePath, branchName, newTitle string) error {
	log.Info("📋 Starting to update PR title for branch %s from worktree: %s", branchName, worktreePath)

	// Get current description first in case we need to prepend overflow
	currentDescription, err := g.GetPRDescriptionInWorktree(worktreePath, branchName)
	if err != nil {
		log.Warn("⚠️ Failed to get current PR description: %v", err)
		currentDescription = ""
	}

	// Validate and truncate PR title if necessary
	validationResult := ValidateAndTruncatePRTitle(newTitle, currentDescription)

	if validationResult.DescriptionPrefix != "" {
		log.Warn("⚠️ PR title exceeded %d characters, truncating", MaxGitHubPRTitleLength)
		newDescription := validationResult.DescriptionPrefix + currentDescription
		if err := g.UpdatePRDescriptionInWorktree(worktreePath, branchName, newDescription); err != nil {
			return fmt.Errorf("failed to update PR description with overflow: %w", err)
		}
	}

	cmd := exec.Command("gh", "pr", "edit", branchName, "--title", validationResult.Title)
	cmd.Dir = worktreePath
	output, err := g.executeWithRetryInDir(cmd, worktreePath, "update PR title")

	if err != nil {
		log.Error("❌ Failed to update PR title: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("failed to update PR title: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully updated PR title")
	return nil
}

// UpdatePRDescriptionInWorktree updates the PR description for a branch from the worktree context
func (g *GitClient) UpdatePRDescriptionInWorktree(worktreePath, branchName, newDescription string) error {
	log.Info("📋 Starting to update PR description for branch %s from worktree: %s", branchName, worktreePath)

	cmd := exec.Command("gh", "pr", "edit", branchName, "--body", newDescription)
	cmd.Dir = worktreePath
	output, err := g.executeWithRetryInDir(cmd, worktreePath, "update PR description")

	if err != nil {
		log.Error("❌ Failed to update PR description: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("failed to update PR description: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully updated PR description")
	return nil
}

// FindPRTemplateInWorktree searches for GitHub PR template in the worktree
func (g *GitClient) FindPRTemplateInWorktree(worktreePath string) (string, error) {
	log.Info("🔍 Searching for GitHub PR template in worktree: %s", worktreePath)

	templatePaths := []string{
		"pull_request_template.md",
		"PULL_REQUEST_TEMPLATE.md",
		".github/pull_request_template.md",
		".github/PULL_REQUEST_TEMPLATE.md",
		"docs/pull_request_template.md",
		"docs/PULL_REQUEST_TEMPLATE.md",
		".github/PULL_REQUEST_TEMPLATE/pull_request_template.md",
	}

	for _, path := range templatePaths {
		fullPath := filepath.Join(worktreePath, path)
		if content, err := os.ReadFile(fullPath); err == nil {
			log.Info("✅ Found PR template at: %s", fullPath)
			return strings.TrimSpace(string(content)), nil
		}
	}

	log.Info("ℹ️ No PR template found in worktree")
	return "", nil
}

// =============================================================================
// Additional Worktree Pool Support Methods
// =============================================================================

// ResetHardInWorktreeToRef performs a git reset --hard to a specific ref in the worktree
func (g *GitClient) ResetHardInWorktreeToRef(worktreePath, ref string) error {
	log.Info("📋 Starting to reset worktree %s to ref %s", worktreePath, ref)

	cmd := exec.Command("git", "reset", "--hard", ref)
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Git reset hard to ref failed in worktree: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("git reset hard to ref failed in worktree: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully reset worktree to ref %s", ref)
	return nil
}

// RenameBranchInWorktree renames a branch within a worktree context
func (g *GitClient) RenameBranchInWorktree(worktreePath, oldBranch, newBranch string) error {
	log.Info("📋 Starting to rename branch %s to %s in worktree %s", oldBranch, newBranch, worktreePath)

	cmd := exec.Command("git", "branch", "-m", oldBranch, newBranch)
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Branch rename failed in worktree: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("branch rename failed in worktree: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully renamed branch to %s", newBranch)
	return nil
}

// GetOriginCommit gets the commit hash of a branch on origin
func (g *GitClient) GetOriginCommit(branchName string) (string, error) {
	log.Info("📋 Starting to get origin commit for branch %s", branchName)

	cmd := exec.Command("git", "rev-parse", fmt.Sprintf("origin/%s", branchName))
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to get origin commit: %v\nOutput: %s", err, string(output))
		return "", fmt.Errorf("failed to get origin commit: %w\nOutput: %s", err, string(output))
	}

	commitHash := strings.TrimSpace(string(output))
	log.Info("✅ Origin commit for %s: %s", branchName, commitHash[:8])
	return commitHash, nil
}

// MoveWorktree moves a worktree to a new path using git worktree move.
// This properly updates git's internal tracking unlike a simple os.Rename.
func (g *GitClient) MoveWorktree(oldPath, newPath string) error {
	log.Info("📋 Starting to move worktree from %s to %s", oldPath, newPath)

	cmd := exec.Command("git", "worktree", "move", oldPath, newPath)
	g.setWorkDir(cmd)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Error("❌ Failed to move worktree: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("failed to move worktree: %w\nOutput: %s", err, string(output))
	}

	log.Info("✅ Successfully moved worktree to %s", newPath)
	return nil
}
