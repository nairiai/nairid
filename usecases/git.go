package usecases

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lucasepe/codename"

	"eksecd/clients"
	"eksecd/core/log"
	"eksecd/models"
	"eksecd/services"
)

type GitUseCase struct {
	gitClient     *clients.GitClient
	claudeService services.CLIAgent
	appState      *models.AppState
	lastGHToken   string
	worktreePool  *WorktreePool
}

type CLIAgentResult struct {
	Output string
	Err    error
}

type AutoCommitResult struct {
	JustCreatedPR   bool
	PullRequestLink string
	PullRequestID   string // GitHub PR number (e.g., "123")
	CommitHash      string
	RepositoryURL   string
	BranchName      string
}

func NewGitUseCase(
	gitClient *clients.GitClient,
	claudeService services.CLIAgent,
	appState *models.AppState,
) *GitUseCase {
	return &GitUseCase{
		gitClient:     gitClient,
		claudeService: claudeService,
		appState:      appState,
	}
}

// getPlatformFromLink returns the platform name based on the message link URL.
// Returns "Discord thread" for Discord URLs, "Slack thread" for everything else.
func getPlatformFromLink(link string) string {
	if strings.Contains(link, "discord.com") || strings.Contains(link, "discord.gg") {
		return "Discord thread"
	}
	return "Slack thread"
}

func (g *GitUseCase) GithubTokenUpdateHook() {
	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Debug("No-repo mode: Skipping GitHub token update hook")
		return
	}

	// Get the GitHub token from environment
	ghToken := os.Getenv("GH_TOKEN")
	if ghToken == "" {
		log.Debug("No GH_TOKEN environment variable found, skipping remote URL update")
		return
	}

	// Only update if token has changed
	if ghToken == g.lastGHToken {
		log.Debug("GH_TOKEN unchanged, skipping remote URL update")
		return
	}

	log.Info("🔄 GH_TOKEN changed, updating Git remote URL with new token")
	if err := g.gitClient.UpdateRemoteURLWithToken(ghToken); err != nil {
		log.Error("Failed to update Git remote URL with token: %v", err)
		// Don't fail the entire reload process, just log the error
		return
	}

	// Store the new token after successful update
	g.lastGHToken = ghToken
	log.Info("✅ Successfully updated Git remote URL with refreshed token")
}

func (g *GitUseCase) ValidateGitEnvironment() error {
	log.Info("📋 Starting to validate Git environment")

	// Check if we're in a Git repository
	if err := g.gitClient.IsGitRepository(); err != nil {
		log.Error("❌ Not in a Git repository: %v", err)
		return fmt.Errorf("eksecd must be run from within a Git repository: %w", err)
	}

	// Check if we're at the Git repository root
	if err := g.gitClient.IsGitRepositoryRoot(); err != nil {
		log.Error("❌ Not at Git repository root: %v", err)
		return fmt.Errorf("eksecd must be run from the Git repository root: %w", err)
	}

	// Check if remote repository exists
	if err := g.gitClient.HasRemoteRepository(); err != nil {
		log.Error("❌ No remote repository configured: %v", err)
		return fmt.Errorf("git repository must have a remote configured: %w", err)
	}

	// Check if GitHub CLI is available (for PR creation)
	if err := g.gitClient.IsGitHubCLIAvailable(); err != nil {
		log.Error("❌ GitHub CLI not available: %v", err)
		return fmt.Errorf("GitHub CLI (gh) must be installed and configured: %w", err)
	}

	// Validate remote repository access credentials
	if err := g.gitClient.ValidateRemoteAccess(); err != nil {
		log.Error("❌ Remote repository access validation failed: %v", err)
		return fmt.Errorf("remote repository access validation failed: %w", err)
	}

	// Get and store repository identifier
	repoIdentifier, err := g.gitClient.GetRepositoryIdentifier()
	if err != nil {
		log.Error("❌ Failed to get repository identifier: %v", err)
		return fmt.Errorf("failed to get repository identifier: %w", err)
	}

	// Update repository context with identifier
	repoCtx := g.appState.GetRepositoryContext()
	repoCtx.RepositoryIdentifier = repoIdentifier
	g.appState.SetRepositoryContext(repoCtx)
	log.Info("📦 Repository identifier: %s", repoIdentifier)

	log.Info("✅ Git environment validation passed")
	log.Info("📋 Completed successfully - validated Git environment")
	return nil
}

// PullLatestChanges pulls latest changes on the current branch
// If the remote branch has been deleted, this returns a special error that should be handled
// by abandoning the job and switching to the default branch
func (g *GitUseCase) PullLatestChanges() error {
	log.Info("📋 Starting to pull latest changes")

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping pull latest changes")
		return nil
	}

	if err := g.gitClient.PullLatest(); err != nil {
		// Check if error is due to remote branch being deleted
		// This likely means the PR was merged or branch was manually removed
		// The caller should abandon the job and clean up
		if strings.Contains(err.Error(), "remote branch deleted") {
			log.Warn("⚠️ Remote branch was deleted - job should be abandoned")
			return fmt.Errorf("remote branch deleted, cannot continue job: %w", err)
		}

		log.Error("❌ Failed to pull latest changes: %v", err)
		return fmt.Errorf("failed to pull latest changes: %w", err)
	}

	log.Info("✅ Successfully pulled latest changes")
	log.Info("📋 Completed successfully - pulled latest changes")
	return nil
}

func (g *GitUseCase) SwitchToJobBranch(branchName string) error {
	log.Info("📋 Starting to switch to job branch: %s", branchName)

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping branch switch")
		return nil
	}

	// Step 1: Reset hard current branch to discard uncommitted changes
	if err := g.gitClient.ResetHard(); err != nil {
		log.Error("❌ Failed to reset hard: %v", err)
		return fmt.Errorf("failed to reset hard: %w", err)
	}

	// Step 2: Clean untracked files
	if err := g.gitClient.CleanUntracked(); err != nil {
		log.Error("❌ Failed to clean untracked files: %v", err)
		return fmt.Errorf("failed to clean untracked files: %w", err)
	}

	// Step 3: Get default branch and checkout to it
	defaultBranch, err := g.gitClient.GetDefaultBranch()
	if err != nil {
		log.Error("❌ Failed to get default branch: %v", err)
		return fmt.Errorf("failed to get default branch: %w", err)
	}

	if err := g.gitClient.CheckoutBranch(defaultBranch); err != nil {
		log.Error("❌ Failed to checkout default branch %s: %v", defaultBranch, err)
		return fmt.Errorf("failed to checkout default branch %s: %w", defaultBranch, err)
	}

	// Step 4: Pull latest changes (this should always succeed on default branch)
	if err := g.gitClient.PullLatest(); err != nil {
		log.Error("❌ Failed to pull latest changes: %v", err)
		return fmt.Errorf("failed to pull latest changes: %w", err)
	}

	// Step 5: Checkout target branch
	// First check if the branch exists locally
	localBranches, err := g.gitClient.GetLocalBranches()
	if err != nil {
		log.Error("❌ Failed to get local branches: %v", err)
		return fmt.Errorf("failed to get local branches: %w", err)
	}

	branchExistsLocally := false
	for _, branch := range localBranches {
		if branch == branchName {
			branchExistsLocally = true
			break
		}
	}

	// Prune stale worktree references before checkout
	// This handles cases where a worktree directory was deleted but git still has a reference
	if err := g.gitClient.PruneWorktrees(); err != nil {
		log.Warn("⚠️ Failed to prune stale worktrees: %v", err)
		// Continue anyway - this is a best-effort cleanup
	}

	if branchExistsLocally {
		// Branch exists locally, checkout normally
		if err := g.gitClient.CheckoutBranch(branchName); err != nil {
			log.Error("❌ Failed to checkout local branch %s: %v", branchName, err)
			return fmt.Errorf("failed to checkout target branch %s: %w", branchName, err)
		}
	} else {
		// Branch doesn't exist locally, check if it exists on remote
		log.Info("ℹ️ Branch %s not found locally, checking remote", branchName)

		remoteExists, err := g.gitClient.RemoteBranchExists(branchName)
		if err != nil {
			log.Error("❌ Failed to check if remote branch exists %s: %v", branchName, err)
			return fmt.Errorf("failed to check if remote branch exists %s: %w", branchName, err)
		}

		if !remoteExists {
			log.Error("❌ Branch %s not found locally or on remote", branchName)
			return fmt.Errorf("branch %s not found locally or on remote", branchName)
		}

		// Branch exists on remote, fetch and checkout
		log.Info("✅ Branch %s found on remote, fetching and checking out", branchName)
		if err := g.gitClient.CheckoutRemoteBranch(branchName); err != nil {
			log.Error("❌ Failed to checkout remote branch %s: %v", branchName, err)
			return fmt.Errorf("failed to checkout target branch %s: %w", branchName, err)
		}
	}

	log.Info("✅ Successfully switched to job branch: %s", branchName)
	log.Info("📋 Completed successfully - switched to job branch")
	return nil
}

func (g *GitUseCase) PrepareForNewConversation(conversationHint string) (string, error) {
	log.Info("📋 Starting to prepare for new conversation")

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping branch creation")
		return "", nil // Return empty branch name in no-repo mode
	}

	// Generate random branch name
	branchName, err := g.generateRandomBranchName()
	if err != nil {
		log.Error("❌ Failed to generate random branch name: %v", err)
		return "", fmt.Errorf("failed to generate branch name: %w", err)
	}

	log.Info("🌿 Generated branch name: %s", branchName)

	// Use the common branch switching logic to get to main and pull latest
	if err := g.resetAndPullDefaultBranch(); err != nil {
		log.Error("❌ Failed to reset and pull main: %v", err)
		return "", fmt.Errorf("failed to reset and pull main: %w", err)
	}

	// Create and checkout new branch
	if err := g.gitClient.CreateAndCheckoutBranch(branchName); err != nil {
		log.Error("❌ Failed to create and checkout new branch %s: %v", branchName, err)
		return "", fmt.Errorf("failed to create and checkout new branch %s: %w", branchName, err)
	}

	log.Info("✅ Successfully prepared for new conversation on branch: %s", branchName)
	log.Info("📋 Completed successfully - prepared for new conversation")
	return branchName, nil
}

// resetAndPullDefaultBranch is a helper that resets current branch, goes to main, and pulls latest
func (g *GitUseCase) resetAndPullDefaultBranch() error {
	log.Info("📋 Starting to reset and pull default branch")

	// Step 1: Reset hard current branch to discard uncommitted changes
	if err := g.gitClient.ResetHard(); err != nil {
		log.Error("❌ Failed to reset hard: %v", err)
		return fmt.Errorf("failed to reset hard: %w", err)
	}

	// Step 2: Clean untracked files
	if err := g.gitClient.CleanUntracked(); err != nil {
		log.Error("❌ Failed to clean untracked files: %v", err)
		return fmt.Errorf("failed to clean untracked files: %w", err)
	}

	// Step 3: Get default branch and checkout to it
	defaultBranch, err := g.gitClient.GetDefaultBranch()
	if err != nil {
		log.Error("❌ Failed to get default branch: %v", err)
		return fmt.Errorf("failed to get default branch: %w", err)
	}

	if err := g.gitClient.CheckoutBranch(defaultBranch); err != nil {
		log.Error("❌ Failed to checkout default branch %s: %v", defaultBranch, err)
		return fmt.Errorf("failed to checkout default branch %s: %w", defaultBranch, err)
	}

	// Step 4: Pull latest changes (should always succeed on default branch)
	// If we hit the remote branch deleted error here, it means the default branch itself
	// was deleted which is a critical error
	if err := g.gitClient.PullLatest(); err != nil {
		log.Error("❌ Failed to pull latest changes: %v", err)
		return fmt.Errorf("failed to pull latest changes: %w", err)
	}

	log.Info("✅ Successfully reset and pulled main")
	log.Info("📋 Completed successfully - reset and pulled main")
	return nil
}

func (g *GitUseCase) AutoCommitChangesIfNeeded(threadLink, sessionID string) (*AutoCommitResult, error) {
	log.Info("📋 Starting to auto-commit changes if needed")

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping auto-commit")
		return &AutoCommitResult{}, nil
	}

	// Get current branch first (needed for both cases)
	currentBranch, err := g.gitClient.GetCurrentBranch()
	if err != nil {
		log.Error("❌ Failed to get current branch: %v", err)
		return nil, fmt.Errorf("failed to get current branch: %w", err)
	}

	// Check if there are any uncommitted changes
	hasChanges, err := g.gitClient.HasUncommittedChanges()
	if err != nil {
		log.Error("❌ Failed to check for uncommitted changes: %v", err)
		return nil, fmt.Errorf("failed to check for uncommitted changes: %w", err)
	}

	if !hasChanges {
		log.Info("ℹ️ No uncommitted changes found - skipping auto-commit")
		log.Info("📋 Completed successfully - no changes to commit")
		return &AutoCommitResult{
			JustCreatedPR:   false,
			PullRequestLink: "",
			PullRequestID:   "",
			CommitHash:      "",
			RepositoryURL:   "",
			BranchName:      currentBranch,
		}, nil
	}

	log.Info("✅ Uncommitted changes detected - proceeding with auto-commit")

	// Generate commit message using Claude
	commitMessage, err := g.generateCommitMessageWithClaude(sessionID, currentBranch)
	if err != nil {
		log.Error("❌ Failed to generate commit message with Claude: %v", err)
		return nil, fmt.Errorf("failed to generate commit message with Claude: %w", err)
	}

	log.Info("📝 Generated commit message: %s", commitMessage)

	// Add all changes
	if err := g.gitClient.AddAll(); err != nil {
		log.Error("❌ Failed to add all changes: %v", err)
		return nil, fmt.Errorf("failed to add all changes: %w", err)
	}

	// Commit with message
	if err := g.gitClient.Commit(commitMessage); err != nil {
		log.Error("❌ Failed to commit changes: %v", err)
		return nil, fmt.Errorf("failed to commit changes: %w", err)
	}

	// Get commit hash after successful commit
	commitHash, err := g.gitClient.GetLatestCommitHash()
	if err != nil {
		log.Error("❌ Failed to get commit hash: %v", err)
		return nil, fmt.Errorf("failed to get commit hash: %w", err)
	}

	// Get repository URL for commit link
	repositoryURL, err := g.gitClient.GetRemoteURL()
	if err != nil {
		log.Error("❌ Failed to get repository URL: %v", err)
		return nil, fmt.Errorf("failed to get repository URL: %w", err)
	}

	// Push current branch to remote
	if err := g.gitClient.PushBranch(currentBranch); err != nil {
		log.Error("❌ Failed to push branch %s: %v", currentBranch, err)
		return nil, fmt.Errorf("failed to push branch %s: %w", currentBranch, err)
	}

	// Handle PR creation/update
	prResult, err := g.handlePRCreationOrUpdate(sessionID, currentBranch, threadLink)
	if err != nil {
		log.Error("❌ Failed to handle PR creation/update: %v", err)
		return nil, fmt.Errorf("failed to handle PR creation/update: %w", err)
	}

	// Update the result with commit information
	prResult.CommitHash = commitHash
	prResult.RepositoryURL = repositoryURL

	// Extract and store PR ID from the PR URL if available
	if prResult.PullRequestLink != "" {
		prResult.PullRequestID = g.gitClient.ExtractPRIDFromURL(prResult.PullRequestLink)
	}

	log.Info("✅ Successfully auto-committed and pushed changes")
	log.Info("📋 Completed successfully - auto-committed changes")
	return prResult, nil
}

func (g *GitUseCase) generateRandomBranchName() (string, error) {
	log.Info("🎲 Generating random branch name")

	rng, err := codename.DefaultRNG()
	if err != nil {
		return "", fmt.Errorf("failed to create random generator: %w", err)
	}

	randomName := codename.Generate(rng, 0)
	timestamp := time.Now().Format("20060102-150405")
	finalBranchName := fmt.Sprintf("eksecd/%s-%s", randomName, timestamp)

	log.Info("🎲 Generated random name: %s", finalBranchName)
	return finalBranchName, nil
}

func (g *GitUseCase) generateCommitMessageWithClaude(sessionID, branchName string) (string, error) {
	log.Info("🤖 Asking Claude to generate commit message")

	prompt := CommitMessageGenerationPrompt(branchName)

	result, err := g.claudeService.ContinueConversation(sessionID, prompt)
	if err != nil {
		return "", fmt.Errorf("claude failed to generate commit message: %w", err)
	}

	return strings.TrimSpace(result.Output), nil
}

func (g *GitUseCase) handlePRCreationOrUpdate(sessionID, branchName, threadLink string) (*AutoCommitResult, error) {
	log.Info("📋 Starting to handle PR creation or update for branch: %s", branchName)

	// Check if a PR already exists for this branch
	hasExistingPR, err := g.gitClient.HasExistingPR(branchName)
	if err != nil {
		log.Error("❌ Failed to check for existing PR: %v", err)
		return nil, fmt.Errorf("failed to check for existing PR: %w", err)
	}

	if hasExistingPR {
		log.Info("✅ Existing PR found for branch %s - changes have been pushed", branchName)

		// Get the PR URL for the existing PR
		prURL, err := g.gitClient.GetPRURL(branchName)
		if err != nil {
			log.Error("❌ Failed to get PR URL for existing PR: %v", err)
			// Continue without the URL rather than failing
			prURL = ""
		}

		// Update PR title and description based on new changes
		if err := g.updatePRTitleAndDescriptionIfNeeded(sessionID, branchName, threadLink); err != nil {
			log.Error("❌ Failed to update PR title/description: %v", err)
			// Log error but don't fail the entire operation
		}

		log.Info("📋 Completed successfully - updated existing PR")
		return &AutoCommitResult{
			JustCreatedPR:   false,
			PullRequestLink: prURL,
			PullRequestID:   g.gitClient.ExtractPRIDFromURL(prURL),
			CommitHash:      "", // Will be filled in by caller
			RepositoryURL:   "", // Will be filled in by caller
			BranchName:      branchName,
		}, nil
	}

	log.Info("🆕 No existing PR found - creating new PR")

	// Generate PR title and body using Claude in parallel
	titleChan := make(chan CLIAgentResult)
	bodyChan := make(chan CLIAgentResult)

	// Start PR title generation
	go func() {
		output, err := g.generatePRTitleWithClaude(sessionID, branchName)
		titleChan <- CLIAgentResult{Output: output, Err: err}
	}()

	// Start PR body generation
	go func() {
		output, err := g.generatePRBodyWithClaude(sessionID, branchName, threadLink)
		bodyChan <- CLIAgentResult{Output: output, Err: err}
	}()

	// Wait for both to complete and collect results
	titleRes := <-titleChan
	bodyRes := <-bodyChan

	// Check for errors
	if titleRes.Err != nil {
		log.Error("❌ Failed to generate PR title with Claude: %v", titleRes.Err)
		return nil, fmt.Errorf("failed to generate PR title with Claude: %w", titleRes.Err)
	}

	if bodyRes.Err != nil {
		log.Error("❌ Failed to generate PR body with Claude: %v", bodyRes.Err)
		return nil, fmt.Errorf("failed to generate PR body with Claude: %w", bodyRes.Err)
	}

	prTitle := titleRes.Output
	prBody := bodyRes.Output

	log.Info("📋 Generated PR title: %s", prTitle)

	// Get default branch for PR base
	defaultBranch, err := g.gitClient.GetDefaultBranch()
	if err != nil {
		log.Error("❌ Failed to get default branch: %v", err)
		return nil, fmt.Errorf("failed to get default branch: %w", err)
	}

	// Create pull request
	prURL, err := g.gitClient.CreatePullRequest(prTitle, prBody, defaultBranch)
	if err != nil {
		log.Error("❌ Failed to create pull request: %v", err)
		return nil, fmt.Errorf("failed to create pull request: %w", err)
	}

	log.Info("✅ Successfully created PR: %s", prTitle)
	log.Info("📋 Completed successfully - created new PR")
	return &AutoCommitResult{
		JustCreatedPR:   true,
		PullRequestLink: prURL,
		PullRequestID:   g.gitClient.ExtractPRIDFromURL(prURL),
		CommitHash:      "", // Will be filled in by caller
		RepositoryURL:   "", // Will be filled in by caller
		BranchName:      branchName,
	}, nil
}

func (g *GitUseCase) generatePRTitleWithClaude(sessionID, branchName string) (string, error) {
	log.Info("🤖 Asking Claude to generate PR title")

	prompt := PRTitleGenerationPrompt(branchName)

	result, err := g.claudeService.ContinueConversation(sessionID, prompt)
	if err != nil {
		return "", fmt.Errorf("claude failed to generate PR title: %w", err)
	}

	return strings.TrimSpace(result.Output), nil
}

func (g *GitUseCase) generatePRBodyWithClaude(sessionID, branchName, threadLink string) (string, error) {
	log.Info("🤖 Asking Claude to generate PR body")

	// Look for GitHub PR template
	prTemplate, err := g.gitClient.FindPRTemplate()
	if err != nil {
		log.Error("⚠️ Failed to search for PR template: %v (continuing with default)", err)
		prTemplate = ""
	}

	prompt := PRDescriptionGenerationPrompt(branchName, prTemplate)

	result, err := g.claudeService.ContinueConversation(sessionID, prompt)
	if err != nil {
		return "", fmt.Errorf("claude failed to generate PR body: %w", err)
	}

	// Append footer with thread link
	cleanBody := strings.TrimSpace(result.Output)
	platformName := getPlatformFromLink(threadLink)
	finalBody := cleanBody + fmt.Sprintf(
		"\n\n---\nGenerated by [eksecd](https://eksec.ai) from this [%s](%s)",
		platformName, threadLink,
	)

	return finalBody, nil
}

// ValidateAndRestorePRDescriptionFooter validates and restores the PR description footer.
// If knownBranchName is non-empty, the branch is already known to have a PR, skipping redundant API calls.
func (g *GitUseCase) ValidateAndRestorePRDescriptionFooter(threadLink, knownBranchName string) error {
	log.Info("📋 Starting to validate and restore PR description footer")

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping PR description footer validation")
		return nil
	}

	var currentBranch string
	if knownBranchName != "" {
		// Caller already knows the branch has a PR, skip redundant API calls
		currentBranch = knownBranchName
		log.Info("📋 Using cached branch name: %s (PR already known to exist)", currentBranch)
	} else {
		// Get current branch
		var err error
		currentBranch, err = g.gitClient.GetCurrentBranch()
		if err != nil {
			log.Error("❌ Failed to get current branch: %v", err)
			return fmt.Errorf("failed to get current branch: %w", err)
		}

		// Check if a PR exists for this branch
		hasExistingPR, err := g.gitClient.HasExistingPR(currentBranch)
		if err != nil {
			log.Error("❌ Failed to check for existing PR: %v", err)
			return fmt.Errorf("failed to check for existing PR: %w", err)
		}

		if !hasExistingPR {
			log.Info("ℹ️ No existing PR found - skipping footer validation")
			log.Info("📋 Completed successfully - no PR to validate")
			return nil
		}
	}

	// Get current PR description
	currentDescription, err := g.gitClient.GetPRDescription(currentBranch)
	if err != nil {
		log.Error("❌ Failed to get PR description: %v", err)
		return fmt.Errorf("failed to get PR description: %w", err)
	}

	// Check if the expected footer pattern is present (using regex to handle different permalinks and platforms)
	footerPattern := `---\s*\n.*Generated by \[eksecd\]\(https://eksec\.ai\) from this \[(Slack|Discord) thread\]\([^)]+\)`

	matched, err := regexp.MatchString(footerPattern, currentDescription)
	if err != nil {
		log.Error("❌ Failed to match footer pattern: %v", err)
		return fmt.Errorf("failed to match footer pattern: %w", err)
	}

	if matched {
		log.Info("✅ PR description already has correct eksecd footer")
		log.Info("📋 Completed successfully - footer validation passed")
		return nil
	}

	log.Info("🔧 PR description missing eksecd footer - restoring it")

	// Remove any existing footer lines to avoid duplicates
	lines := strings.Split(currentDescription, "\n")
	var cleanedLines []string
	inFooterSection := false

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Check if we've hit a footer section
		if strings.Contains(trimmedLine, "Generated by [eksecd]") ||
			strings.Contains(trimmedLine, "Generated by Claude Code") {
			inFooterSection = true
			continue
		}

		// Skip separator lines that are part of footer
		if trimmedLine == "---" {
			// Look ahead to see if this separator is followed by footer content
			isFooterSeparator := false
			for i := len(cleanedLines); i < len(lines)-1; i++ {
				nextLine := strings.TrimSpace(lines[i+1])
				if nextLine == "" {
					continue
				}
				if strings.Contains(nextLine, "Generated by [eksecd]") {
					isFooterSeparator = true
				}
				break
			}

			if isFooterSeparator || inFooterSection {
				continue
			}
		}

		// Skip empty lines in footer section
		if inFooterSection && trimmedLine == "" {
			continue
		}

		cleanedLines = append(cleanedLines, line)
	}

	// Remove trailing empty lines
	for len(cleanedLines) > 0 && strings.TrimSpace(cleanedLines[len(cleanedLines)-1]) == "" {
		cleanedLines = cleanedLines[:len(cleanedLines)-1]
	}

	// Add the correct footer
	platformName := getPlatformFromLink(threadLink)
	expectedFooter := fmt.Sprintf(
		"Generated by [eksecd](https://eksec.ai) from this [%s](%s)",
		platformName, threadLink,
	)
	restoredDescription := strings.Join(cleanedLines, "\n")
	if restoredDescription != "" {
		// Check if description already ends with a separator
		if strings.HasSuffix(strings.TrimSpace(restoredDescription), "---") {
			restoredDescription += "\n" + expectedFooter
		} else {
			restoredDescription += "\n\n---\n" + expectedFooter
		}
	} else {
		restoredDescription = "---\n" + expectedFooter
	}

	// Update the PR description
	if err := g.gitClient.UpdatePRDescription(currentBranch, restoredDescription); err != nil {
		log.Error("❌ Failed to update PR description: %v", err)
		return fmt.Errorf("failed to update PR description: %w", err)
	}

	log.Info("✅ Successfully restored eksecd footer to PR description")
	log.Info("📋 Completed successfully - restored PR description footer")
	return nil
}

func (g *GitUseCase) CheckPRStatus(branchName string) (string, error) {
	log.Info("📋 Starting to check PR status for branch: %s", branchName)

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping PR status check")
		return "no_pr", nil
	}

	// Handle empty branch name (can happen for jobs created in no-repo mode)
	if branchName == "" {
		log.Info("ℹ️ Empty branch name - skipping PR status check")
		return "no_pr", nil
	}

	// First check if a PR exists for this branch
	hasExistingPR, err := g.gitClient.HasExistingPR(branchName)
	if err != nil {
		log.Error("❌ Failed to check for existing PR for branch %s: %v", branchName, err)
		return "", fmt.Errorf("failed to check for existing PR: %w", err)
	}

	if !hasExistingPR {
		log.Info("📋 No PR found for branch %s", branchName)
		return "no_pr", nil
	}

	// Get PR status using GitHub CLI
	prStatus, err := g.gitClient.GetPRState(branchName)
	if err != nil {
		log.Error("❌ Failed to get PR state for branch %s: %v", branchName, err)
		return "", fmt.Errorf("failed to get PR state: %w", err)
	}

	log.Info("📋 Completed successfully - PR status for branch %s: %s", branchName, prStatus)
	return prStatus, nil
}

func (g *GitUseCase) CheckPRStatusByID(prID string) (string, error) {
	log.Info("📋 Starting to check PR status by ID: %s", prID)

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping PR status check by ID")
		return "no_pr", nil
	}

	// Get PR status directly by PR ID using GitHub CLI
	prStatus, err := g.gitClient.GetPRStateByID(prID)
	if err != nil {
		log.Error("❌ Failed to get PR state for PR ID %s: %v", prID, err)
		return "", fmt.Errorf("failed to get PR state by ID: %w", err)
	}

	log.Info("📋 Completed successfully - PR status for ID %s: %s", prID, prStatus)
	return prStatus, nil
}

func (g *GitUseCase) CleanupStaleBranches() error {
	log.Info("📋 Starting to cleanup stale eksecd branches")

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping branch cleanup")
		return nil
	}

	// Get all local branches
	localBranches, err := g.gitClient.GetLocalBranches()
	if err != nil {
		log.Error("❌ Failed to get local branches: %v", err)
		return fmt.Errorf("failed to get local branches: %w", err)
	}

	// Get current branch to avoid deleting it
	currentBranch, err := g.gitClient.GetCurrentBranch()
	if err != nil {
		log.Error("❌ Failed to get current branch: %v", err)
		return fmt.Errorf("failed to get current branch: %w", err)
	}

	// Get default branch to avoid deleting it
	defaultBranch, err := g.gitClient.GetDefaultBranch()
	if err != nil {
		log.Error("❌ Failed to get default branch: %v", err)
		return fmt.Errorf("failed to get default branch: %w", err)
	}

	// Get all tracked job branches from app state
	trackedJobs := g.appState.GetAllJobs()
	trackedBranches := make(map[string]bool)
	for _, jobData := range trackedJobs {
		if jobData.BranchName != "" {
			trackedBranches[jobData.BranchName] = true
		}
	}

	// Filter branches for cleanup
	var branchesToDelete []string
	protectedBranches := []string{"main", "master", currentBranch, defaultBranch}

	for _, branch := range localBranches {
		// Only process eksecd/ branches
		if !strings.HasPrefix(branch, "eksecd/") {
			continue
		}

		// Skip protected branches
		isProtected := false
		for _, protected := range protectedBranches {
			if branch == protected {
				isProtected = true
				break
			}
		}
		if isProtected {
			log.Info("⚠️ Skipping protected branch: %s", branch)
			continue
		}

		// Skip tracked branches
		if trackedBranches[branch] {
			log.Info("⚠️ Skipping tracked branch: %s", branch)
			continue
		}

		// Skip pool worktree branches (managed by worktree pool)
		if strings.HasPrefix(branch, "eksecd/pool-ready-") {
			log.Info("⚠️ Skipping pool branch: %s", branch)
			continue
		}

		// This branch is stale - mark for deletion
		branchesToDelete = append(branchesToDelete, branch)
	}

	if len(branchesToDelete) == 0 {
		log.Info("✅ No stale eksecd branches found")
		log.Info("📋 Completed successfully - no stale branches to cleanup")
		return nil
	}

	log.Info("🧹 Found %d stale eksecd branches to delete", len(branchesToDelete))

	// Delete each stale branch
	deletedCount := 0
	for _, branch := range branchesToDelete {
		if err := g.gitClient.DeleteLocalBranch(branch); err != nil {
			log.Error("❌ Failed to delete stale branch %s: %v", branch, err)
			// Continue with other branches even if one fails
			continue
		}
		deletedCount++
		log.Info("🗑️ Deleted stale branch: %s", branch)
	}

	log.Info("✅ Successfully deleted %d out of %d stale eksecd branches", deletedCount, len(branchesToDelete))
	log.Info("📋 Completed successfully - cleaned up stale branches")
	return nil
}

func (g *GitUseCase) updatePRTitleAndDescriptionIfNeeded(sessionID, branchName, threadLink string) error {
	log.Info("📋 Starting to update PR title and description if needed for branch: %s", branchName)

	// Get current PR title and description
	currentTitle, err := g.gitClient.GetPRTitle(branchName)
	if err != nil {
		log.Error("❌ Failed to get current PR title: %v", err)
		return fmt.Errorf("failed to get current PR title: %w", err)
	}

	currentDescription, err := g.gitClient.GetPRDescription(branchName)
	if err != nil {
		log.Error("❌ Failed to get current PR description: %v", err)
		return fmt.Errorf("failed to get current PR description: %w", err)
	}

	// Generate updated PR title and description using Claude in parallel
	titleUpdateChan := make(chan CLIAgentResult)
	descriptionUpdateChan := make(chan CLIAgentResult)

	// Start updated PR title generation
	go func() {
		output, err := g.generateUpdatedPRTitleWithClaude(sessionID, branchName, currentTitle)
		titleUpdateChan <- CLIAgentResult{Output: output, Err: err}
	}()

	// Start updated PR description generation
	go func() {
		output, err := g.generateUpdatedPRDescriptionWithClaude(
			sessionID,
			branchName,
			currentDescription,
			threadLink,
		)
		descriptionUpdateChan <- CLIAgentResult{Output: output, Err: err}
	}()

	// Wait for both to complete and collect results
	titleUpdateRes := <-titleUpdateChan
	descriptionUpdateRes := <-descriptionUpdateChan

	// Check for errors
	if titleUpdateRes.Err != nil {
		log.Error("❌ Failed to generate updated PR title with Claude: %v", titleUpdateRes.Err)
		return fmt.Errorf("failed to generate updated PR title with Claude: %w", titleUpdateRes.Err)
	}

	if descriptionUpdateRes.Err != nil {
		log.Error("❌ Failed to generate updated PR description with Claude: %v", descriptionUpdateRes.Err)
		return fmt.Errorf("failed to generate updated PR description with Claude: %w", descriptionUpdateRes.Err)
	}

	updatedTitle := titleUpdateRes.Output
	updatedDescription := descriptionUpdateRes.Output

	// Update title if it has changed
	if strings.TrimSpace(updatedTitle) != strings.TrimSpace(currentTitle) {
		log.Info("🔄 PR title has changed, updating...")
		if err := g.gitClient.UpdatePRTitle(branchName, updatedTitle); err != nil {
			log.Error("❌ Failed to update PR title: %v", err)
			return fmt.Errorf("failed to update PR title: %w", err)
		}
		log.Info("✅ Successfully updated PR title")
	} else {
		log.Info("ℹ️ PR title remains the same - no update needed")
	}

	// Update description if it has changed
	if strings.TrimSpace(updatedDescription) != strings.TrimSpace(currentDescription) {
		log.Info("🔄 PR description has changed, updating...")
		if err := g.gitClient.UpdatePRDescription(branchName, updatedDescription); err != nil {
			log.Error("❌ Failed to update PR description: %v", err)
			return fmt.Errorf("failed to update PR description: %w", err)
		}
		log.Info("✅ Successfully updated PR description")
	} else {
		log.Info("ℹ️ PR description remains the same - no update needed")
	}

	log.Info("📋 Completed successfully - updated PR title and description if needed")
	return nil
}

func (g *GitUseCase) generateUpdatedPRTitleWithClaude(sessionID, branchName, currentTitle string) (string, error) {
	log.Info("🤖 Asking Claude to generate updated PR title")

	prompt := PRTitleUpdatePrompt(currentTitle, branchName)

	result, err := g.claudeService.ContinueConversation(sessionID, prompt)
	if err != nil {
		return "", fmt.Errorf("claude failed to generate updated PR title: %w", err)
	}

	return strings.TrimSpace(result.Output), nil
}

func (g *GitUseCase) generateUpdatedPRDescriptionWithClaude(
	sessionID, branchName, currentDescription, threadLink string,
) (string, error) {
	log.Info("🤖 Asking Claude to generate updated PR description")

	// Remove existing footer from current description for analysis
	currentDescriptionClean := g.removeFooterFromDescription(currentDescription)

	prompt := PRDescriptionUpdatePrompt(currentDescriptionClean, branchName)

	result, err := g.claudeService.ContinueConversation(sessionID, prompt)
	if err != nil {
		return "", fmt.Errorf("claude failed to generate updated PR description: %w", err)
	}

	// Append footer with thread link
	cleanBody := strings.TrimSpace(result.Output)
	platformName := getPlatformFromLink(threadLink)
	finalBody := cleanBody + fmt.Sprintf(
		"\n\n---\nGenerated by [eksecd](https://eksec.ai) from this [%s](%s)",
		platformName, threadLink,
	)

	return finalBody, nil
}

func (g *GitUseCase) removeFooterFromDescription(description string) string {
	// Remove the eksecd footer to get clean description for analysis
	footerPattern := `---\s*\n.*Generated by \[eksecd\]\(https://eksec\.ai\) from this \[(Slack|Discord) thread\]\([^)]+\)`

	// Use regex to remove the footer section
	re := regexp.MustCompile(footerPattern)
	cleanDescription := re.ReplaceAllString(description, "")

	// Clean up any trailing whitespace
	return strings.TrimSpace(cleanDescription)
}

// BranchExists checks if a branch exists locally
func (g *GitUseCase) BranchExists(branchName string) (bool, error) {
	log.Info("📋 Checking if branch %s exists", branchName)

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Branch check returns false (no branches)")
		return false, nil
	}

	// Get all local branches
	localBranches, err := g.gitClient.GetLocalBranches()
	if err != nil {
		log.Error("❌ Failed to get local branches: %v", err)
		return false, fmt.Errorf("failed to get local branches: %w", err)
	}

	// Check if the branch is in the list
	for _, branch := range localBranches {
		if branch == branchName {
			log.Info("✅ Branch %s exists", branchName)
			return true, nil
		}
	}

	log.Info("ℹ️ Branch %s does not exist", branchName)
	return false, nil
}

// AbandonJobAndCleanup abandons a job due to deleted remote branch
// This resets to the default branch and deletes the local branch
func (g *GitUseCase) AbandonJobAndCleanup(jobID, branchName string) error {
	log.Info("📋 Starting to abandon job %s and cleanup branch %s", jobID, branchName)

	// Remove job from app state first (always do this, even in no-repo mode)
	if err := g.appState.RemoveJob(jobID); err != nil {
		log.Error("❌ Failed to remove job %s from state: %v", jobID, err)
		return fmt.Errorf("failed to remove job from state: %w", err)
	}

	// Check if we're in repo mode - skip git operations if not
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping branch cleanup for abandoned job")
		return nil
	}

	// Switch to default branch to clean up state
	if err := g.resetAndPullDefaultBranch(); err != nil {
		log.Error("❌ Failed to reset and pull default branch: %v", err)
		return fmt.Errorf("failed to reset to default branch: %w", err)
	}

	// Delete the local branch if it exists
	branchExists, err := g.BranchExists(branchName)
	if err != nil {
		log.Error("❌ Failed to check if branch %s exists: %v", branchName, err)
		return fmt.Errorf("failed to check if branch exists: %w", err)
	}

	if branchExists {
		if err := g.gitClient.DeleteLocalBranch(branchName); err != nil {
			log.Error("❌ Failed to delete local branch %s: %v", branchName, err)
			return fmt.Errorf("failed to delete local branch: %w", err)
		}
		log.Info("🗑️ Deleted local branch: %s", branchName)
	}

	log.Info("✅ Successfully abandoned job and cleaned up")
	log.Info("📋 Completed successfully - abandoned job and reset to default branch")
	return nil
}

// =============================================================================
// Worktree-based Concurrent Job Support
// =============================================================================

// GetWorktreeBasePath returns the base path for eksecd worktrees.
// Worktrees are stored in ~/.eksec_worktrees/
// If AGENT_EXEC_USER is set (managed mode), worktrees are stored in that user's home
// directory to ensure they persist on the mounted volume.
func (g *GitUseCase) GetWorktreeBasePath() (string, error) {
	// In managed mode, use the agent execution user's home for persistent storage
	if execUser := os.Getenv("AGENT_EXEC_USER"); execUser != "" {
		return filepath.Join("/home", execUser, ".eksec_worktrees"), nil
	}

	// Default: use current user's home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	return filepath.Join(homeDir, ".eksec_worktrees"), nil
}

// GetMaxConcurrency returns the max concurrency setting from environment
// Defaults to 1 (sequential processing) if not set
func (g *GitUseCase) GetMaxConcurrency() int {
	maxConcurrency := 1
	if envVal := os.Getenv("MAX_CONCURRENCY"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val > 0 {
			maxConcurrency = val
		}
	}
	return maxConcurrency
}

// ShouldUseWorktrees returns true if concurrent worktree mode should be used
func (g *GitUseCase) ShouldUseWorktrees() bool {
	return g.GetMaxConcurrency() > 1
}

// PrepareForNewConversationWithWorktree creates a worktree for a new conversation
// Returns (branchName, worktreePath, error)
// This is used when MAX_CONCURRENCY > 1 for concurrent job processing
//
// If a worktree pool is configured, this function will first try to acquire
// a pre-warmed worktree from the pool for instant assignment. If the pool
// is empty or acquisition fails, it falls back to synchronous creation.
//
// If a worktree already exists for the given jobID (e.g., due to message retries),
// it will be cleaned up first before creating/acquiring a new one.
func (g *GitUseCase) PrepareForNewConversationWithWorktree(jobID, conversationHint string) (string, string, error) {
	log.Info("📋 Starting to prepare worktree for new conversation (jobID: %s)", jobID)

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping worktree creation")
		return "", "", nil
	}

	// Check if a worktree already exists for this jobID and clean it up
	// This handles cases where start_conversation is sent multiple times for the same job
	// (e.g., due to message retries or race conditions)
	worktreeBasePath, err := g.GetWorktreeBasePath()
	if err != nil {
		log.Error("❌ Failed to get worktree base path: %v", err)
		return "", "", fmt.Errorf("failed to get worktree base path: %w", err)
	}

	existingWorktreePath := filepath.Join(worktreeBasePath, jobID)
	if g.gitClient.WorktreeExists(existingWorktreePath) {
		log.Warn("⚠️ Worktree already exists for jobID %s at %s - cleaning up before creating new one", jobID, existingWorktreePath)

		// Get the branch name from the existing worktree for cleanup
		existingBranch, branchErr := g.gitClient.GetCurrentBranchInWorktree(existingWorktreePath)
		if branchErr != nil {
			log.Warn("⚠️ Failed to get branch name from existing worktree: %v", branchErr)
			existingBranch = "" // Will skip branch deletion
		}

		// Clean up the existing worktree
		if cleanupErr := g.CleanupJobWorktree(existingWorktreePath, existingBranch); cleanupErr != nil {
			log.Error("❌ Failed to cleanup existing worktree for jobID %s: %v", jobID, cleanupErr)
			return "", "", fmt.Errorf("failed to cleanup existing worktree: %w", cleanupErr)
		}

		log.Info("✅ Successfully cleaned up existing worktree for jobID %s", jobID)
	}

	// Generate random branch name
	branchName, err := g.generateRandomBranchName()
	if err != nil {
		log.Error("❌ Failed to generate random branch name: %v", err)
		return "", "", fmt.Errorf("failed to generate branch name: %w", err)
	}

	log.Info("🌿 Generated branch name: %s", branchName)

	// Try to acquire from pool first (if pool is configured)
	if g.worktreePool != nil {
		worktreePath, err := g.worktreePool.Acquire(jobID, branchName)
		if err == nil {
			log.Info("🏊 Acquired worktree from pool: %s", worktreePath)
			return branchName, worktreePath, nil
		}
		if !errors.Is(err, ErrPoolEmpty) && !errors.Is(err, ErrWorktreeInvalid) {
			// Unexpected error - fail fast
			return "", "", fmt.Errorf("pool acquire failed: %w", err)
		}
		if errors.Is(err, ErrWorktreeInvalid) {
			log.Warn("⚠️ Pool worktree was invalid (likely stale after container restart), falling back to sync creation")
		} else {
			log.Info("ℹ️ Pool empty, creating worktree synchronously")
		}
	}

	// Fallback: create synchronously (existing logic)
	log.Info("🔨 Creating worktree synchronously...")

	// Reset main repo to default branch before creating worktree to prevent
	// cross-pollination of changes between worktrees. This ensures the main
	// repository is in a clean, known state when spawning new worktrees.
	if err := g.resetAndPullDefaultBranch(); err != nil {
		log.Error("❌ Failed to reset main repo to default branch before worktree creation: %v", err)
		return "", "", fmt.Errorf("failed to reset main repo before worktree creation: %w", err)
	}

	// Fetch latest from origin (safe for concurrent calls)
	if err := g.gitClient.FetchOrigin(); err != nil {
		log.Error("❌ Failed to fetch from origin: %v", err)
		return "", "", fmt.Errorf("failed to fetch from origin: %w", err)
	}

	// Get default branch name
	defaultBranch, err := g.gitClient.GetDefaultBranch()
	if err != nil {
		log.Error("❌ Failed to get default branch: %v", err)
		return "", "", fmt.Errorf("failed to get default branch: %w", err)
	}

	// Create base directory if it doesn't exist
	// Note: worktreeBasePath was already retrieved earlier for existing worktree check
	if err := os.MkdirAll(worktreeBasePath, 0755); err != nil {
		log.Error("❌ Failed to create worktree base directory: %v", err)
		return "", "", fmt.Errorf("failed to create worktree base directory: %w", err)
	}

	worktreePath := filepath.Join(worktreeBasePath, jobID)

	// Create worktree with new branch based on origin/<default-branch>
	baseRef := fmt.Sprintf("origin/%s", defaultBranch)
	if err := g.gitClient.CreateWorktree(worktreePath, branchName, baseRef); err != nil {
		log.Error("❌ Failed to create worktree: %v", err)
		return "", "", fmt.Errorf("failed to create worktree: %w", err)
	}

	log.Info("✅ Successfully created worktree at %s for branch %s", worktreePath, branchName)
	return branchName, worktreePath, nil
}

// SetWorktreePool sets the worktree pool for fast worktree acquisition
func (g *GitUseCase) SetWorktreePool(pool *WorktreePool) {
	g.worktreePool = pool
}

// GetWorktreePool returns the worktree pool (may be nil if not configured)
func (g *GitUseCase) GetWorktreePool() *WorktreePool {
	return g.worktreePool
}

// GetGitClient returns the underlying git client (for pool initialization)
func (g *GitUseCase) GetGitClient() *clients.GitClient {
	return g.gitClient
}

// PrepareWorktreeForJob validates and prepares an existing worktree for continuing a job
func (g *GitUseCase) PrepareWorktreeForJob(worktreePath, branchName string) error {
	log.Info("📋 Starting to prepare worktree for job: %s (branch: %s)", worktreePath, branchName)

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping worktree preparation")
		return nil
	}

	// Check if worktree exists
	if !g.gitClient.WorktreeExists(worktreePath) {
		log.Error("❌ Worktree does not exist at %s", worktreePath)
		return fmt.Errorf("worktree not found at %s", worktreePath)
	}

	// Pull latest changes in the worktree
	if err := g.gitClient.PullLatestInWorktree(worktreePath); err != nil {
		// Check if error is due to remote branch being deleted
		if strings.Contains(err.Error(), "remote branch deleted") {
			log.Warn("⚠️ Remote branch was deleted for worktree")
			return fmt.Errorf("remote branch deleted, cannot continue job: %w", err)
		}

		log.Error("❌ Failed to pull latest in worktree: %v", err)
		return fmt.Errorf("failed to pull latest in worktree: %w", err)
	}

	log.Info("✅ Successfully prepared worktree for job")
	return nil
}

// CleanupJobWorktree removes the worktree for a completed or abandoned job
func (g *GitUseCase) CleanupJobWorktree(worktreePath, branchName string) error {
	log.Info("📋 Starting to cleanup job worktree: %s", worktreePath)

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping worktree cleanup")
		return nil
	}

	// Check if worktree exists
	if !g.gitClient.WorktreeExists(worktreePath) {
		log.Info("ℹ️ Worktree does not exist at %s - nothing to cleanup", worktreePath)
		return nil
	}

	// Remove worktree
	if err := g.gitClient.RemoveWorktree(worktreePath); err != nil {
		log.Error("❌ Failed to remove worktree: %v", err)
		return fmt.Errorf("failed to remove worktree: %w", err)
	}

	// Delete local branch if it still exists
	branchExists, err := g.BranchExists(branchName)
	if err != nil {
		log.Warn("⚠️ Failed to check if branch %s exists: %v", branchName, err)
		// Continue anyway - branch cleanup is best effort
	}

	if branchExists {
		if err := g.gitClient.DeleteLocalBranch(branchName); err != nil {
			log.Warn("⚠️ Failed to delete local branch %s: %v", branchName, err)
			// Continue anyway - branch cleanup is best effort
		} else {
			log.Info("🗑️ Deleted local branch: %s", branchName)
		}
	}

	log.Info("✅ Successfully cleaned up job worktree: %s", worktreePath)
	return nil
}

// CleanupOrphanedWorktrees removes worktrees that don't correspond to any tracked job
func (g *GitUseCase) CleanupOrphanedWorktrees() error {
	log.Info("📋 Starting to cleanup orphaned worktrees")

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping orphaned worktree cleanup")
		return nil
	}

	// Prune stale worktree entries first
	if err := g.gitClient.PruneWorktrees(); err != nil {
		log.Warn("⚠️ Failed to prune worktrees: %v", err)
		// Continue anyway
	}

	// Get worktree base path
	worktreeBasePath, err := g.GetWorktreeBasePath()
	if err != nil {
		return fmt.Errorf("failed to get worktree base path: %w", err)
	}

	// Check if worktree directory exists
	if _, err := os.Stat(worktreeBasePath); os.IsNotExist(err) {
		log.Info("ℹ️ Worktree base directory doesn't exist - nothing to cleanup")
		return nil
	}

	// List all directories in worktree base path
	entries, err := os.ReadDir(worktreeBasePath)
	if err != nil {
		log.Error("❌ Failed to read worktree directory: %v", err)
		return fmt.Errorf("failed to read worktree directory: %w", err)
	}

	// Get all tracked jobs
	trackedJobs := g.appState.GetAllJobs()
	trackedWorktrees := make(map[string]bool)
	for _, jobData := range trackedJobs {
		if jobData.WorktreePath != "" {
			trackedWorktrees[jobData.WorktreePath] = true
		}
	}

	// Identify orphaned worktrees
	orphanedCount := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Skip pool-* directories - these are managed by the worktree pool
		// and will be reclaimed or cleaned up by the pool itself
		if strings.HasPrefix(entry.Name(), "pool-") {
			log.Debug("⏭️ Skipping pool worktree: %s (managed by pool)", entry.Name())
			continue
		}

		worktreePath := filepath.Join(worktreeBasePath, entry.Name())
		if !trackedWorktrees[worktreePath] {
			log.Info("🗑️ Found orphaned worktree: %s", worktreePath)

			// Remove the worktree
			if err := g.gitClient.RemoveWorktree(worktreePath); err != nil {
				log.Warn("⚠️ Failed to remove orphaned worktree %s: %v", worktreePath, err)
				// Continue with other worktrees
			} else {
				orphanedCount++
			}
		}
	}

	log.Info("✅ Cleaned up %d orphaned worktrees", orphanedCount)
	return nil
}

// WorktreeExists checks if a worktree exists at the given path
func (g *GitUseCase) WorktreeExists(worktreePath string) bool {
	return g.gitClient.WorktreeExists(worktreePath)
}

// AutoCommitChangesInWorktreeIfNeeded auto-commits changes in a specific worktree
func (g *GitUseCase) AutoCommitChangesInWorktreeIfNeeded(
	threadLink, sessionID, worktreePath string,
) (*AutoCommitResult, error) {
	log.Info("📋 Starting to auto-commit changes in worktree: %s", worktreePath)

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping auto-commit")
		return &AutoCommitResult{}, nil
	}

	// Get current branch in worktree
	currentBranch, err := g.gitClient.GetCurrentBranchInWorktree(worktreePath)
	if err != nil {
		log.Error("❌ Failed to get current branch in worktree: %v", err)
		return nil, fmt.Errorf("failed to get current branch in worktree: %w", err)
	}

	// Check if there are any uncommitted changes in worktree
	hasChanges, err := g.gitClient.HasUncommittedChangesInWorktree(worktreePath)
	if err != nil {
		log.Error("❌ Failed to check for uncommitted changes in worktree: %v", err)
		return nil, fmt.Errorf("failed to check for uncommitted changes in worktree: %w", err)
	}

	if !hasChanges {
		log.Info("ℹ️ No uncommitted changes found in worktree - skipping auto-commit")
		return &AutoCommitResult{
			JustCreatedPR:   false,
			PullRequestLink: "",
			PullRequestID:   "",
			CommitHash:      "",
			RepositoryURL:   "",
			BranchName:      currentBranch,
		}, nil
	}

	log.Info("✅ Uncommitted changes detected in worktree - proceeding with auto-commit")

	// Generate commit message using Claude (in the worktree directory)
	commitMessage, err := g.generateCommitMessageWithClaudeInWorktree(sessionID, currentBranch, worktreePath)
	if err != nil {
		log.Error("❌ Failed to generate commit message with Claude: %v", err)
		return nil, fmt.Errorf("failed to generate commit message with Claude: %w", err)
	}

	log.Info("📝 Generated commit message: %s", commitMessage)

	// Add all changes in worktree
	if err := g.gitClient.AddAllInWorktree(worktreePath); err != nil {
		log.Error("❌ Failed to add all changes in worktree: %v", err)
		return nil, fmt.Errorf("failed to add all changes in worktree: %w", err)
	}

	// Commit with message in worktree
	if err := g.gitClient.CommitInWorktree(worktreePath, commitMessage); err != nil {
		log.Error("❌ Failed to commit changes in worktree: %v", err)
		return nil, fmt.Errorf("failed to commit changes in worktree: %w", err)
	}

	// Get commit hash after successful commit
	commitHash, err := g.gitClient.GetLatestCommitHashInWorktree(worktreePath)
	if err != nil {
		log.Error("❌ Failed to get commit hash in worktree: %v", err)
		return nil, fmt.Errorf("failed to get commit hash in worktree: %w", err)
	}

	// Get repository URL for commit link
	repositoryURL, err := g.gitClient.GetRemoteURLInWorktree(worktreePath)
	if err != nil {
		log.Error("❌ Failed to get repository URL from worktree: %v", err)
		return nil, fmt.Errorf("failed to get repository URL from worktree: %w", err)
	}

	// Push current branch from worktree
	if err := g.gitClient.PushBranchFromWorktree(worktreePath, currentBranch); err != nil {
		log.Error("❌ Failed to push branch from worktree: %v", err)
		return nil, fmt.Errorf("failed to push branch from worktree: %w", err)
	}

	// Handle PR creation/update from worktree context
	prResult, err := g.handlePRCreationOrUpdateInWorktree(sessionID, currentBranch, threadLink, worktreePath)
	if err != nil {
		log.Error("❌ Failed to handle PR creation/update in worktree: %v", err)
		return nil, fmt.Errorf("failed to handle PR creation/update in worktree: %w", err)
	}

	// Update the result with commit information
	prResult.CommitHash = commitHash
	prResult.RepositoryURL = repositoryURL

	// Extract and store PR ID from the PR URL if available
	if prResult.PullRequestLink != "" {
		prResult.PullRequestID = g.gitClient.ExtractPRIDFromURL(prResult.PullRequestLink)
	}

	log.Info("✅ Successfully auto-committed and pushed changes from worktree")
	return prResult, nil
}

func (g *GitUseCase) generateCommitMessageWithClaudeInWorktree(sessionID, branchName, worktreePath string) (string, error) {
	log.Info("🤖 Asking Claude to generate commit message in worktree: %s", worktreePath)

	prompt := CommitMessageGenerationPrompt(branchName)

	// Use the worktree directory for Claude session
	result, err := g.claudeService.ContinueConversationInDir(sessionID, prompt, worktreePath)
	if err != nil {
		return "", fmt.Errorf("claude failed to generate commit message: %w", err)
	}

	return strings.TrimSpace(result.Output), nil
}

func (g *GitUseCase) handlePRCreationOrUpdateInWorktree(
	sessionID, branchName, threadLink, worktreePath string,
) (*AutoCommitResult, error) {
	log.Info("📋 Starting to handle PR creation or update for branch: %s (worktree: %s)", branchName, worktreePath)

	// Check if a PR already exists for this branch
	hasExistingPR, err := g.gitClient.HasExistingPRInWorktree(worktreePath, branchName)
	if err != nil {
		log.Error("❌ Failed to check for existing PR: %v", err)
		return nil, fmt.Errorf("failed to check for existing PR: %w", err)
	}

	if hasExistingPR {
		log.Info("✅ Existing PR found for branch %s - changes have been pushed", branchName)

		// Get the PR URL for the existing PR
		prURL, err := g.gitClient.GetPRURLInWorktree(worktreePath, branchName)
		if err != nil {
			log.Error("❌ Failed to get PR URL for existing PR: %v", err)
			prURL = ""
		}

		// Update PR title and description based on new changes
		if err := g.updatePRTitleAndDescriptionInWorktreeIfNeeded(sessionID, branchName, threadLink, worktreePath); err != nil {
			log.Error("❌ Failed to update PR title/description: %v", err)
			// Log error but don't fail the entire operation
		}

		return &AutoCommitResult{
			JustCreatedPR:   false,
			PullRequestLink: prURL,
			PullRequestID:   g.gitClient.ExtractPRIDFromURL(prURL),
			CommitHash:      "",
			RepositoryURL:   "",
			BranchName:      branchName,
		}, nil
	}

	log.Info("🆕 No existing PR found - creating new PR from worktree")

	// Generate PR title and body using Claude in parallel
	titleChan := make(chan CLIAgentResult)
	bodyChan := make(chan CLIAgentResult)

	go func() {
		output, err := g.generatePRTitleWithClaudeInWorktree(sessionID, branchName, worktreePath)
		titleChan <- CLIAgentResult{Output: output, Err: err}
	}()

	go func() {
		output, err := g.generatePRBodyWithClaudeInWorktree(sessionID, branchName, threadLink, worktreePath)
		bodyChan <- CLIAgentResult{Output: output, Err: err}
	}()

	titleRes := <-titleChan
	bodyRes := <-bodyChan

	if titleRes.Err != nil {
		log.Error("❌ Failed to generate PR title with Claude: %v", titleRes.Err)
		return nil, fmt.Errorf("failed to generate PR title with Claude: %w", titleRes.Err)
	}

	if bodyRes.Err != nil {
		log.Error("❌ Failed to generate PR body with Claude: %v", bodyRes.Err)
		return nil, fmt.Errorf("failed to generate PR body with Claude: %w", bodyRes.Err)
	}

	prTitle := titleRes.Output
	prBody := bodyRes.Output

	log.Info("📋 Generated PR title: %s", prTitle)

	// Get default branch for PR base
	defaultBranch, err := g.gitClient.GetDefaultBranchInWorktree(worktreePath)
	if err != nil {
		log.Error("❌ Failed to get default branch: %v", err)
		return nil, fmt.Errorf("failed to get default branch: %w", err)
	}

	// Create pull request from worktree
	prURL, err := g.gitClient.CreatePullRequestInWorktree(worktreePath, prTitle, prBody, defaultBranch)
	if err != nil {
		log.Error("❌ Failed to create pull request: %v", err)
		return nil, fmt.Errorf("failed to create pull request: %w", err)
	}

	log.Info("✅ Successfully created PR: %s", prTitle)
	return &AutoCommitResult{
		JustCreatedPR:   true,
		PullRequestLink: prURL,
		PullRequestID:   g.gitClient.ExtractPRIDFromURL(prURL),
		CommitHash:      "",
		RepositoryURL:   "",
		BranchName:      branchName,
	}, nil
}

func (g *GitUseCase) generatePRTitleWithClaudeInWorktree(sessionID, branchName, worktreePath string) (string, error) {
	log.Info("🤖 Asking Claude to generate PR title in worktree: %s", worktreePath)

	prompt := PRTitleGenerationPrompt(branchName)

	result, err := g.claudeService.ContinueConversationInDir(sessionID, prompt, worktreePath)
	if err != nil {
		return "", fmt.Errorf("claude failed to generate PR title: %w", err)
	}

	return strings.TrimSpace(result.Output), nil
}

func (g *GitUseCase) generatePRBodyWithClaudeInWorktree(
	sessionID, branchName, threadLink, worktreePath string,
) (string, error) {
	log.Info("🤖 Asking Claude to generate PR body in worktree: %s", worktreePath)

	// Look for GitHub PR template in worktree
	prTemplate, err := g.gitClient.FindPRTemplateInWorktree(worktreePath)
	if err != nil {
		log.Error("⚠️ Failed to search for PR template: %v (continuing with default)", err)
		prTemplate = ""
	}

	prompt := PRDescriptionGenerationPrompt(branchName, prTemplate)

	result, err := g.claudeService.ContinueConversationInDir(sessionID, prompt, worktreePath)
	if err != nil {
		return "", fmt.Errorf("claude failed to generate PR body: %w", err)
	}

	// Append footer with thread link
	cleanBody := strings.TrimSpace(result.Output)
	platformName := getPlatformFromLink(threadLink)
	finalBody := cleanBody + fmt.Sprintf(
		"\n\n---\nGenerated by [eksecd](https://eksec.ai) from this [%s](%s)",
		platformName, threadLink,
	)

	return finalBody, nil
}

func (g *GitUseCase) updatePRTitleAndDescriptionInWorktreeIfNeeded(
	sessionID, branchName, threadLink, worktreePath string,
) error {
	log.Info("📋 Starting to update PR title and description if needed (worktree: %s)", worktreePath)

	// Get current PR title and description
	currentTitle, err := g.gitClient.GetPRTitleInWorktree(worktreePath, branchName)
	if err != nil {
		log.Error("❌ Failed to get current PR title: %v", err)
		return fmt.Errorf("failed to get current PR title: %w", err)
	}

	currentDescription, err := g.gitClient.GetPRDescriptionInWorktree(worktreePath, branchName)
	if err != nil {
		log.Error("❌ Failed to get current PR description: %v", err)
		return fmt.Errorf("failed to get current PR description: %w", err)
	}

	// Generate updated PR title and description using Claude in parallel
	titleUpdateChan := make(chan CLIAgentResult)
	descriptionUpdateChan := make(chan CLIAgentResult)

	go func() {
		output, err := g.generateUpdatedPRTitleWithClaudeInWorktree(sessionID, branchName, currentTitle, worktreePath)
		titleUpdateChan <- CLIAgentResult{Output: output, Err: err}
	}()

	go func() {
		output, err := g.generateUpdatedPRDescriptionWithClaudeInWorktree(
			sessionID, branchName, currentDescription, threadLink, worktreePath,
		)
		descriptionUpdateChan <- CLIAgentResult{Output: output, Err: err}
	}()

	titleUpdateRes := <-titleUpdateChan
	descriptionUpdateRes := <-descriptionUpdateChan

	if titleUpdateRes.Err != nil {
		log.Error("❌ Failed to generate updated PR title: %v", titleUpdateRes.Err)
		return fmt.Errorf("failed to generate updated PR title: %w", titleUpdateRes.Err)
	}

	if descriptionUpdateRes.Err != nil {
		log.Error("❌ Failed to generate updated PR description: %v", descriptionUpdateRes.Err)
		return fmt.Errorf("failed to generate updated PR description: %w", descriptionUpdateRes.Err)
	}

	updatedTitle := titleUpdateRes.Output
	updatedDescription := descriptionUpdateRes.Output

	// Update title if changed
	if strings.TrimSpace(updatedTitle) != strings.TrimSpace(currentTitle) {
		log.Info("🔄 PR title has changed, updating...")
		if err := g.gitClient.UpdatePRTitleInWorktree(worktreePath, branchName, updatedTitle); err != nil {
			log.Error("❌ Failed to update PR title: %v", err)
			return fmt.Errorf("failed to update PR title: %w", err)
		}
		log.Info("✅ Successfully updated PR title")
	} else {
		log.Info("ℹ️ PR title remains the same - no update needed")
	}

	// Update description if changed
	if strings.TrimSpace(updatedDescription) != strings.TrimSpace(currentDescription) {
		log.Info("🔄 PR description has changed, updating...")
		if err := g.gitClient.UpdatePRDescriptionInWorktree(worktreePath, branchName, updatedDescription); err != nil {
			log.Error("❌ Failed to update PR description: %v", err)
			return fmt.Errorf("failed to update PR description: %w", err)
		}
		log.Info("✅ Successfully updated PR description")
	} else {
		log.Info("ℹ️ PR description remains the same - no update needed")
	}

	log.Info("📋 Completed successfully - updated PR title and description if needed")
	return nil
}

func (g *GitUseCase) generateUpdatedPRTitleWithClaudeInWorktree(
	sessionID, branchName, currentTitle, worktreePath string,
) (string, error) {
	log.Info("🤖 Asking Claude to generate updated PR title in worktree: %s", worktreePath)

	prompt := PRTitleUpdatePrompt(currentTitle, branchName)

	result, err := g.claudeService.ContinueConversationInDir(sessionID, prompt, worktreePath)
	if err != nil {
		return "", fmt.Errorf("claude failed to generate updated PR title: %w", err)
	}

	return strings.TrimSpace(result.Output), nil
}

func (g *GitUseCase) generateUpdatedPRDescriptionWithClaudeInWorktree(
	sessionID, branchName, currentDescription, threadLink, worktreePath string,
) (string, error) {
	log.Info("🤖 Asking Claude to generate updated PR description in worktree: %s", worktreePath)

	currentDescriptionClean := g.removeFooterFromDescription(currentDescription)

	prompt := PRDescriptionUpdatePrompt(currentDescriptionClean, branchName)

	result, err := g.claudeService.ContinueConversationInDir(sessionID, prompt, worktreePath)
	if err != nil {
		return "", fmt.Errorf("claude failed to generate updated PR description: %w", err)
	}

	// Append footer with thread link
	cleanBody := strings.TrimSpace(result.Output)
	platformName := getPlatformFromLink(threadLink)
	finalBody := cleanBody + fmt.Sprintf(
		"\n\n---\nGenerated by [eksecd](https://eksec.ai) from this [%s](%s)",
		platformName, threadLink,
	)

	return finalBody, nil
}

// ValidateAndRestorePRDescriptionFooterInWorktree validates and restores PR footer in worktree.
// If knownBranchName is non-empty, the branch is already known to have a PR, skipping redundant API calls.
func (g *GitUseCase) ValidateAndRestorePRDescriptionFooterInWorktree(threadLink, worktreePath, knownBranchName string) error {
	log.Info("📋 Starting to validate and restore PR description footer in worktree: %s", worktreePath)

	// Check if we're in repo mode
	repoContext := g.appState.GetRepositoryContext()
	if !repoContext.IsRepoMode {
		log.Info("📦 No-repo mode: Skipping PR description footer validation")
		return nil
	}

	var currentBranch string
	if knownBranchName != "" {
		// Caller already knows the branch has a PR, skip redundant API calls
		currentBranch = knownBranchName
		log.Info("📋 Using cached branch name: %s (PR already known to exist)", currentBranch)
	} else {
		// Get current branch in worktree
		var err error
		currentBranch, err = g.gitClient.GetCurrentBranchInWorktree(worktreePath)
		if err != nil {
			log.Error("❌ Failed to get current branch in worktree: %v", err)
			return fmt.Errorf("failed to get current branch in worktree: %w", err)
		}

		// Check if a PR exists for this branch
		hasExistingPR, err := g.gitClient.HasExistingPRInWorktree(worktreePath, currentBranch)
		if err != nil {
			log.Error("❌ Failed to check for existing PR: %v", err)
			return fmt.Errorf("failed to check for existing PR: %w", err)
		}

		if !hasExistingPR {
			log.Info("ℹ️ No existing PR found - skipping footer validation")
			return nil
		}
	}

	// Get current PR description
	currentDescription, err := g.gitClient.GetPRDescriptionInWorktree(worktreePath, currentBranch)
	if err != nil {
		log.Error("❌ Failed to get PR description: %v", err)
		return fmt.Errorf("failed to get PR description: %w", err)
	}

	// Check if the expected footer pattern is present
	footerPattern := `---\s*\n.*Generated by \[eksecd\]\(https://eksec\.ai\) from this \[(Slack|Discord) thread\]\([^)]+\)`

	matched, err := regexp.MatchString(footerPattern, currentDescription)
	if err != nil {
		log.Error("❌ Failed to match footer pattern: %v", err)
		return fmt.Errorf("failed to match footer pattern: %w", err)
	}

	if matched {
		log.Info("✅ PR description already has correct eksecd footer")
		return nil
	}

	log.Info("🔧 PR description missing eksecd footer - restoring it")

	// Remove any existing footer lines to avoid duplicates
	lines := strings.Split(currentDescription, "\n")
	var cleanedLines []string
	inFooterSection := false

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		if strings.Contains(trimmedLine, "Generated by [eksecd]") ||
			strings.Contains(trimmedLine, "Generated by Claude Code") {
			inFooterSection = true
			continue
		}

		if trimmedLine == "---" {
			isFooterSeparator := false
			for i := len(cleanedLines); i < len(lines)-1; i++ {
				nextLine := strings.TrimSpace(lines[i+1])
				if nextLine == "" {
					continue
				}
				if strings.Contains(nextLine, "Generated by [eksecd]") {
					isFooterSeparator = true
				}
				break
			}

			if isFooterSeparator || inFooterSection {
				continue
			}
		}

		if inFooterSection && trimmedLine == "" {
			continue
		}

		cleanedLines = append(cleanedLines, line)
	}

	// Remove trailing empty lines
	for len(cleanedLines) > 0 && strings.TrimSpace(cleanedLines[len(cleanedLines)-1]) == "" {
		cleanedLines = cleanedLines[:len(cleanedLines)-1]
	}

	// Add the correct footer
	platformName := getPlatformFromLink(threadLink)
	expectedFooter := fmt.Sprintf(
		"Generated by [eksecd](https://eksec.ai) from this [%s](%s)",
		platformName, threadLink,
	)
	restoredDescription := strings.Join(cleanedLines, "\n")
	if restoredDescription != "" {
		if strings.HasSuffix(strings.TrimSpace(restoredDescription), "---") {
			restoredDescription += "\n" + expectedFooter
		} else {
			restoredDescription += "\n\n---\n" + expectedFooter
		}
	} else {
		restoredDescription = "---\n" + expectedFooter
	}

	// Update the PR description
	if err := g.gitClient.UpdatePRDescriptionInWorktree(worktreePath, currentBranch, restoredDescription); err != nil {
		log.Error("❌ Failed to update PR description: %v", err)
		return fmt.Errorf("failed to update PR description: %w", err)
	}

	log.Info("✅ Successfully restored eksecd footer to PR description")
	return nil
}
