package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gammazero/workerpool"
	"github.com/jessevdk/go-flags"
	"github.com/zishang520/socket.io/clients/socket/v3"
	"github.com/zishang520/socket.io/v3/pkg/types"

	"eksecd/clients"
	claudeclient "eksecd/clients/claude"
	codexclient "eksecd/clients/codex"
	cursorclient "eksecd/clients/cursor"
	opencodeclient "eksecd/clients/opencode"
	"eksecd/core"
	"eksecd/core/env"
	"eksecd/core/log"
	"eksecd/handlers"
	"eksecd/models"
	"eksecd/services"
	claudeservice "eksecd/services/claude"
	codexservice "eksecd/services/codex"
	cursorservice "eksecd/services/cursor"
	opencodeservice "eksecd/services/opencode"
	"eksecd/usecases"
	"eksecd/utils"
)

type CmdRunner struct {
	messageHandler  *handlers.MessageHandler
	messageSender   *handlers.MessageSender
	connectionState *handlers.ConnectionState
	gitUseCase      *usecases.GitUseCase
	appState        *models.AppState
	rotatingWriter  *log.RotatingWriter
	envManager      *env.EnvManager
	agentID         string
	agentsApiClient *clients.AgentsApiClient
	wsURL           string
	eksecAPIKey     string
	dirLock         *utils.DirLock
	repoLock        *utils.DirLock

	// Persistent worker pools reused across reconnects
	blockingWorkerPool *workerpool.WorkerPool
	instantWorkerPool  *workerpool.WorkerPool

	// Job dispatcher for per-job message sequencing
	dispatcher *handlers.JobDispatcher

	// Worktree pool for fast worktree acquisition
	poolCtx    context.Context
	poolCancel context.CancelFunc
}

// validateModelForAgent checks if the specified model is compatible with the chosen agent
func validateModelForAgent(agentType, model string) error {
	// If no model specified, it's valid for all agents (they'll use defaults)
	if model == "" {
		return nil
	}

	switch agentType {
	case "claude":
		// Claude accepts model aliases (sonnet, haiku, opus) or full model names
		// No specific validation needed - Claude CLI will handle invalid model names
	case "cursor":
		// Validate Cursor models
		validCursorModels := map[string]bool{
			"gpt-5":             true,
			"sonnet-4":          true,
			"sonnet-4-thinking": true,
		}
		if !validCursorModels[model] {
			return fmt.Errorf("--model '%s' is not valid for cursor agent (valid options: gpt-5, sonnet-4, sonnet-4-thinking)", model)
		}
	case "codex":
		// Codex accepts any model string (default: gpt-5)
		// No specific validation needed as it's flexible
	case "opencode":
		// OpenCode expects provider/model format (default: opencode/grok-code)
		if !strings.Contains(model, "/") {
			return fmt.Errorf("--model '%s' is not valid for opencode agent (expected format: provider/model, e.g., opencode/grok-code)", model)
		}
	default:
		return fmt.Errorf("unknown agent type: %s", agentType)
	}

	return nil
}

// fetchAndSetToken fetches the token from API and sets it as environment variable
func fetchAndSetToken(agentsApiClient *clients.AgentsApiClient, envManager *env.EnvManager) error {
	// Skip token operations for self-hosted installations
	if agentsApiClient.IsSelfHosted() {
		log.Info("🏠 Self-hosted installation detected, skipping token fetch")
		return nil
	}

	// Skip token operations when running with secret proxy (managed container mode)
	// In this mode, the secret proxy handles token fetching and injection via HTTP MITM.
	if clients.AgentHTTPProxy() != "" {
		log.Info("🔒 Secret proxy mode detected, skipping token fetch (proxy handles secrets)")
		return nil
	}

	log.Info("🔑 Fetching Anthropic token from API...")

	tokenResp, err := agentsApiClient.FetchToken()
	if err != nil {
		return fmt.Errorf("failed to fetch token: %w", err)
	}

	// Set the token as environment variable using EnvManager
	if err := envManager.Set(tokenResp.EnvKey, tokenResp.Token); err != nil {
		return fmt.Errorf("failed to set environment variable %s: %w", tokenResp.EnvKey, err)
	}

	log.Info("✅ Successfully fetched and set token (env key: %s, expires: %s)",
		tokenResp.EnvKey, tokenResp.ExpiresAt.Format(time.RFC3339))

	return nil
}

// fetchAndStoreArtifacts fetches agent artifacts from API and stores them locally
func fetchAndStoreArtifacts(agentsApiClient *clients.AgentsApiClient) error {
	log.Info("📦 Fetching agent artifacts from API...")

	// Clean up existing rules, MCP configs, and skills before downloading new ones
	// This ensures stale items deleted on the server are removed locally
	if err := utils.CleanCcagentRulesDir(); err != nil {
		return fmt.Errorf("failed to clean eksecd rules directory: %w", err)
	}

	// Skip MCP config cleanup and download when MCP proxy is enabled
	// (configs will be fetched from the proxy at processing time)
	if clients.AgentMCPProxy() == "" {
		if err := utils.CleanCcagentMCPDir(); err != nil {
			return fmt.Errorf("failed to clean eksecd MCP directory: %w", err)
		}
	} else {
		log.Info("📦 MCP proxy enabled, skipping MCP config artifact downloads")
	}

	if err := utils.CleanCcagentSkillsDir(); err != nil {
		return fmt.Errorf("failed to clean eksecd skills directory: %w", err)
	}

	artifacts, err := agentsApiClient.FetchArtifacts()
	if err != nil {
		return fmt.Errorf("failed to fetch artifacts: %w", err)
	}

	// Handle empty artifacts list
	if len(artifacts) == 0 {
		log.Info("📦 No artifacts configured for this agent")
		return nil
	}

	log.Info("📦 Found %d artifact(s) to download", len(artifacts))

	// Check if MCP proxy is enabled (skip MCP config artifact downloads)
	mcpProxyEnabled := clients.AgentMCPProxy() != ""

	// Download and store each artifact file
	for _, artifact := range artifacts {
		// Skip MCP config artifacts when proxy is enabled
		// (the proxy fetches its own configs directly from the backend)
		if mcpProxyEnabled && (artifact.Type == "mcp_config" || artifact.Type == "mcp_cfg") {
			log.Info("📦 Skipping %s artifact (MCP proxy handles this): %s", artifact.Type, artifact.Title)
			continue
		}

		log.Info("📦 Processing %s artifact: %s (%s)", artifact.Type, artifact.Title, artifact.Description)

		for _, file := range artifact.Files {
			log.Info("📥 Downloading artifact file to: %s", file.Location)

			if err := utils.FetchAndStoreArtifact(agentsApiClient, file.AttachmentID, file.Location); err != nil {
				return fmt.Errorf("failed to download artifact file %s: %w", file.Location, err)
			}

			log.Info("✅ Successfully saved artifact file: %s", file.Location)
		}
	}

	log.Info("✅ Successfully downloaded all artifacts")
	return nil
}

// processAgentRules processes rules from eksecd directory based on agent type
// targetHomeDir specifies the home directory to deploy rules to.
// If empty, uses the current user's home directory.
func processAgentRules(agentType, workDir, targetHomeDir string) error {
	log.Info("📋 Processing agent rules for agent type: %s", agentType)

	var processor utils.RulesProcessor

	switch agentType {
	case "claude":
		processor = utils.NewClaudeCodeRulesProcessor(workDir)
	case "opencode":
		processor = utils.NewOpenCodeRulesProcessor(workDir)
	case "cursor", "codex":
		// Cursor and Codex don't support rules processing yet
		processor = utils.NewNoOpRulesProcessor()
	default:
		return fmt.Errorf("unknown agent type: %s", agentType)
	}

	if err := processor.ProcessRules(targetHomeDir); err != nil {
		return fmt.Errorf("failed to process rules: %w", err)
	}

	return nil
}

// processMCPConfigs processes MCP configs from eksecd directory based on agent type
// targetHomeDir specifies the home directory to deploy configs to.
// If empty, uses the current user's home directory.
func processMCPConfigs(agentType, workDir, targetHomeDir string) error {
	log.Info("🔌 Processing MCP configs for agent type: %s", agentType)

	var processor utils.MCPProcessor

	// Check if MCP proxy mode is enabled
	mcpProxyURL := clients.AgentMCPProxy()
	if mcpProxyURL != "" {
		log.Info("🔌 MCP proxy mode enabled: %s", mcpProxyURL)
		switch agentType {
		case "claude":
			processor = utils.NewClaudeCodeProxiedMCPProcessor(mcpProxyURL)
		case "opencode":
			processor = utils.NewOpenCodeProxiedMCPProcessor(mcpProxyURL)
		case "cursor", "codex":
			processor = utils.NewNoOpMCPProcessor()
		default:
			return fmt.Errorf("unknown agent type: %s", agentType)
		}
	} else {
		switch agentType {
		case "claude":
			processor = utils.NewClaudeCodeMCPProcessor(workDir)
		case "opencode":
			processor = utils.NewOpenCodeMCPProcessor(workDir)
		case "cursor", "codex":
			// Cursor and Codex don't support MCP configs yet
			processor = utils.NewNoOpMCPProcessor()
		default:
			return fmt.Errorf("unknown agent type: %s", agentType)
		}
	}

	if err := processor.ProcessMCPConfigs(targetHomeDir); err != nil {
		return fmt.Errorf("failed to process MCP configs: %w", err)
	}

	return nil
}

// processSkills processes skills from eksecd directory based on agent type
// targetHomeDir specifies the home directory to deploy skills to.
// If empty, uses the current user's home directory.
func processSkills(agentType, targetHomeDir string) error {
	log.Info("🎯 Processing skills for agent type: %s", agentType)

	var processor utils.SkillsProcessor

	switch agentType {
	case "claude":
		processor = utils.NewClaudeCodeSkillsProcessor()
	case "opencode":
		processor = utils.NewOpenCodeSkillsProcessor()
	case "cursor", "codex":
		// Cursor and Codex don't support skills yet
		processor = utils.NewNoOpSkillsProcessor()
	default:
		return fmt.Errorf("unknown agent type: %s", agentType)
	}

	if err := processor.ProcessSkills(targetHomeDir); err != nil {
		return fmt.Errorf("failed to process skills: %w", err)
	}

	return nil
}

// processPermissions configures agent-specific permissions for automated operation
// targetHomeDir specifies the home directory to deploy config to.
// If empty, uses the current user's home directory.
func processPermissions(agentType, workDir, targetHomeDir string) error {
	log.Info("🔓 Processing permissions for agent type: %s", agentType)

	var processor utils.PermissionsProcessor

	switch agentType {
	case "opencode":
		// OpenCode requires explicit permission configuration for yolo mode
		processor = utils.NewOpenCodePermissionsProcessor(workDir)
	case "claude", "cursor", "codex":
		// Claude, Cursor, and Codex handle permissions via CLI flags
		processor = utils.NewNoOpPermissionsProcessor()
	default:
		return fmt.Errorf("unknown agent type: %s", agentType)
	}

	if err := processor.ProcessPermissions(targetHomeDir); err != nil {
		return fmt.Errorf("failed to process permissions: %w", err)
	}

	return nil
}

// resolveRepositoryContext determines the repository mode and path based on the --repo flag
// and current working directory. Returns a RepositoryContext indicating:
// - Repo mode with explicit path (--repo flag provided)
// - Repo mode with auto-detected path (cwd is a git root)
// - No-repo mode (cwd is not a git repository)
func resolveRepositoryContext(repoPath string, gitClient *clients.GitClient) (*models.RepositoryContext, error) {
	if repoPath != "" {
		return resolveExplicitRepoPath(repoPath)
	}
	return resolveAutoDetectedRepoContext(gitClient)
}

func resolveExplicitRepoPath(repoPath string) (*models.RepositoryContext, error) {
	var absRepoPath string
	if filepath.IsAbs(repoPath) {
		absRepoPath = repoPath
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current working directory: %w", err)
		}
		absRepoPath = filepath.Join(cwd, repoPath)
	}

	if _, err := os.Stat(absRepoPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("repository path does not exist: %s", absRepoPath)
	}

	log.Info("📦 Repository mode enabled (explicit): %s", absRepoPath)
	return &models.RepositoryContext{
		RepoPath:   absRepoPath,
		IsRepoMode: true,
	}, nil
}

func resolveAutoDetectedRepoContext(gitClient *clients.GitClient) (*models.RepositoryContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working directory: %w", err)
	}

	if gitClient.IsGitRepositoryRoot() == nil {
		log.Info("📦 Repository mode enabled (auto-detected): %s", cwd)
		return &models.RepositoryContext{
			RepoPath:   cwd,
			IsRepoMode: true,
		}, nil
	}

	log.Info("📦 No-repo mode enabled - not in a git repository")
	return &models.RepositoryContext{
		IsRepoMode: false,
	}, nil
}

func NewCmdRunner(agentType, permissionMode, model, repoPath string) (*CmdRunner, error) {
	log.Info("📋 Starting to initialize CmdRunner with agent: %s", agentType)

	// Validate model compatibility with agent
	if err := validateModelForAgent(agentType, model); err != nil {
		return nil, err
	}

	// Create log directory for agent service
	configDir, err := env.GetConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get config directory: %w", err)
	}
	logDir := filepath.Join(configDir, "logs")

	// Initialize environment manager first
	envManager, err := env.NewEnvManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create environment manager: %w", err)
	}

	// Start periodic refresh every 1 minute
	envManager.StartPeriodicRefresh(1 * time.Minute)

	// Get API key and WS URL for agents API client
	eksecAPIKey := envManager.Get("EKSEC_API_KEY")
	if eksecAPIKey == "" {
		return nil, fmt.Errorf("EKSEC_API_KEY environment variable is required but not set")
	}

	wsURL := envManager.Get("EKSEC_WS_API_URL")
	if wsURL == "" {
		wsURL = "https://api.eksec.ai/socketio/"
	}

	// Extract base URL for API client (remove /socketio/ suffix)
	apiBaseURL := strings.TrimSuffix(wsURL, "/socketio/")
	// Get agent ID for X-AGENT-ID header (used to disambiguate containers sharing API keys)
	agentIDForAPI := envManager.Get("EKSEC_AGENT_ID")
	agentsApiClient := clients.NewAgentsApiClient(eksecAPIKey, apiBaseURL, agentIDForAPI)
	log.Info("🔗 Configured agents API client with base URL: %s", apiBaseURL)

	// Fetch and set Anthropic token BEFORE initializing anything else
	if err := fetchAndSetToken(agentsApiClient, envManager); err != nil {
		return nil, fmt.Errorf("failed to fetch and set token: %w", err)
	}

	// Fetch and store agent artifacts (rules, guidelines, instructions)
	if err := fetchAndStoreArtifacts(agentsApiClient); err != nil {
		return nil, fmt.Errorf("failed to fetch and store artifacts: %w", err)
	}

	// Get current working directory for Codex client
	workDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	// Determine target home directory for artifact deployment.
	// When AGENT_EXEC_USER is set (managed container mode), artifacts should be
	// deployed to that user's home directory since the agent process runs as that user.
	// This ensures skills, rules, and MCP configs are accessible to the agent.
	targetHomeDir := ""
	if execUser := clients.AgentExecUser(); execUser != "" {
		targetHomeDir = "/home/" + execUser
		log.Info("🏠 Agent exec user configured: %s, deploying artifacts to %s", execUser, targetHomeDir)
	}

	// Process rules based on agent type
	if err := processAgentRules(agentType, workDir, targetHomeDir); err != nil {
		return nil, fmt.Errorf("failed to process agent rules: %w", err)
	}

	// Process MCP configs based on agent type
	if err := processMCPConfigs(agentType, workDir, targetHomeDir); err != nil {
		return nil, fmt.Errorf("failed to process MCP configs: %w", err)
	}

	// Process skills based on agent type
	if err := processSkills(agentType, targetHomeDir); err != nil {
		return nil, fmt.Errorf("failed to process skills: %w", err)
	}

	// Process permissions based on agent type (enables yolo mode for OpenCode)
	if err := processPermissions(agentType, workDir, targetHomeDir); err != nil {
		return nil, fmt.Errorf("failed to process permissions: %w", err)
	}

	// Create the appropriate CLI agent service (now with all dependencies available)
	cliAgent, err := createCLIAgent(agentType, permissionMode, model, logDir, workDir, agentsApiClient, envManager)
	if err != nil {
		return nil, fmt.Errorf("failed to create CLI agent: %w", err)
	}

	// Cleanup old session logs (older than 7 days)
	err = cliAgent.CleanupOldLogs(7)
	if err != nil {
		log.Error("Warning: Failed to cleanup old session logs: %v", err)
		// Don't exit - this is not critical for agent operation
	}

	gitClient := clients.NewGitClient()

	// Determine state file path
	statePath := filepath.Join(configDir, "state.json")

	// Restore app state from persisted data
	appState, agentID, err := handlers.RestoreAppState(statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to restore app state: %w", err)
	}

	// Handle repository path and create repository context
	repoContext, err := resolveRepositoryContext(repoPath, gitClient)
	if err != nil {
		return nil, err
	}

	// Set repository context in app state
	appState.SetRepositoryContext(repoContext)

	// Configure gitClient to use repository path from app state
	gitClient.SetRepoPathProvider(func() string {
		ctx := appState.GetRepositoryContext()
		return ctx.RepoPath
	})

	// Initialize ConnectionState and MessageSender
	connectionState := handlers.NewConnectionState()
	messageSender := handlers.NewMessageSender(connectionState)

	gitUseCase := usecases.NewGitUseCase(gitClient, cliAgent, appState)

	messageHandler := handlers.NewMessageHandler(cliAgent, gitUseCase, appState, envManager, messageSender, agentsApiClient)

	// Create the CmdRunner instance
	cr := &CmdRunner{
		messageHandler:  messageHandler,
		messageSender:   messageSender,
		connectionState: connectionState,
		gitUseCase:      gitUseCase,
		appState:        appState,
		envManager:      envManager,
		agentID:         agentID,
		agentsApiClient: agentsApiClient,
		wsURL:           wsURL,
		eksecAPIKey:     eksecAPIKey,
	}

	// Initialize dual worker pools that persist for the app lifetime
	// MAX_CONCURRENCY controls how many concurrent jobs can be processed
	// Default is 1 (sequential processing) for backward compatibility
	maxConcurrency := 1
	if envVal := envManager.Get("MAX_CONCURRENCY"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val > 0 {
			maxConcurrency = val
			log.Info("🔧 MAX_CONCURRENCY set to %d (concurrent job processing enabled)", maxConcurrency)
		}
	}
	cr.blockingWorkerPool = workerpool.New(maxConcurrency) // concurrent conversation processing
	cr.instantWorkerPool = workerpool.New(5)               // parallel PR status checks

	// Initialize job dispatcher for per-job message sequencing
	cr.dispatcher = handlers.NewJobDispatcher(
		cr.messageHandler,
		cr.blockingWorkerPool,
		cr.appState,
	)
	log.Info("🔀 Initialized job dispatcher for per-job message sequencing")

	// Wire up the job evictor so MessageHandler can signal dispatcher to stop failed jobs
	cr.messageHandler.SetJobEvictor(cr.dispatcher)

	// Initialize worktree pool if concurrency enabled and in repo mode
	// Note: repoContext is already set above, we just refresh it here
	var worktreePool *usecases.WorktreePool
	repoContext = appState.GetRepositoryContext()
	if gitUseCase.ShouldUseWorktrees() && repoContext.IsRepoMode {
		// Get pool size from environment, default to MAX_CONCURRENCY
		poolSize := maxConcurrency
		if envVal := envManager.Get("WORKTREE_POOL_SIZE"); envVal != "" {
			if val, err := strconv.Atoi(envVal); err == nil && val > 0 {
				poolSize = val
			}
		}

		worktreeBasePath, err := gitUseCase.GetWorktreeBasePath()
		if err != nil {
			return nil, fmt.Errorf("failed to get worktree base path: %w", err)
		}

		worktreePool = usecases.NewWorktreePool(
			gitUseCase.GetGitClient(),
			worktreeBasePath,
			poolSize,
		)
		gitUseCase.SetWorktreePool(worktreePool)

		// Create context for pool lifecycle
		cr.poolCtx, cr.poolCancel = context.WithCancel(context.Background())

		// Clean up stale job worktrees with broken git references
		// This can happen when containers are recreated - old job worktrees remain
		// but their git links point to non-existent directories
		if err := worktreePool.CleanupStaleJobWorktrees(); err != nil {
			log.Warn("⚠️ Failed to clean up stale job worktrees: %v", err)
		}

		// Reclaim any orphaned pool worktrees from previous crash
		if err := worktreePool.ReclaimOrphanedPoolWorktrees(); err != nil {
			log.Warn("⚠️ Failed to reclaim orphaned pool worktrees: %v", err)
		}

		// Start the pool replenisher
		worktreePool.Start(cr.poolCtx)
		log.Info("🏊 Worktree pool initialized (target size: %d)", poolSize)
	}

	// Register GitHub token update hook
	envManager.RegisterReloadHook(gitUseCase.GithubTokenUpdateHook)
	log.Info("📎 Registered GitHub token update hook")

	// Wait for the worktree pool initial fill to complete before recovering jobs.
	// This prevents a race condition where the pool's resetMainRepoToDefaultBranch()
	// (git reset --hard) runs concurrently with job recovery's SwitchToJobBranch()
	// (also git reset --hard), both competing for .git/index.lock on the same repo.
	if worktreePool != nil {
		worktreePool.WaitForInitialFill()
	}

	// Recover in-progress jobs and queued messages on program startup (NOT on Socket.io reconnect)
	// This enables crash recovery - we only want to recover jobs once when the program starts
	handlers.RecoverJobs(
		appState,
		gitUseCase,
		cr.dispatcher,
		messageHandler,
	)

	log.Info("📋 Completed successfully - initialized CmdRunner with %s agent", agentType)
	return cr, nil
}

// createCLIAgent creates the appropriate CLI agent based on the agent type
func createCLIAgent(
	agentType, permissionMode, model, logDir, workDir string,
	agentsApiClient *clients.AgentsApiClient,
	envManager *env.EnvManager,
) (services.CLIAgent, error) {
	// Apply default models when not specified
	if model == "" {
		switch agentType {
		case "codex":
			model = "gpt-5"
		case "opencode":
			model = "opencode/grok-code"
			// cursor and claude don't need defaults (cursor and claude use empty string for their defaults)
		}
	}

	switch agentType {
	case "claude":
		claudeClient := claudeclient.NewClaudeClient(permissionMode)
		return claudeservice.NewClaudeService(claudeClient, logDir, model, agentsApiClient, envManager), nil
	case "cursor":
		cursorClient := cursorclient.NewCursorClient()
		return cursorservice.NewCursorService(cursorClient, logDir, model), nil
	case "codex":
		codexClient := codexclient.NewCodexClient(permissionMode, workDir)
		return codexservice.NewCodexService(codexClient, logDir, model), nil
	case "opencode":
		opencodeClient := opencodeclient.NewOpenCodeClient()
		return opencodeservice.NewOpenCodeService(opencodeClient, logDir, model), nil
	default:
		return nil, fmt.Errorf("unsupported agent type: %s", agentType)
	}
}

type Options struct {
	//nolint
	Agent             string `long:"agent" description:"CLI agent to use (claude, cursor, codex, or opencode)" choice:"claude" choice:"cursor" choice:"codex" choice:"opencode" default:"claude"`
	BypassPermissions bool   `long:"claude-bypass-permissions" description:"Use bypassPermissions mode for Claude/Codex (only applies when --agent=claude or --agent=codex) (WARNING: Only use in controlled sandbox environments)"`
	Model             string `long:"model" description:"Model to use (agent-specific: claude: sonnet/haiku/opus or full model name, cursor: gpt-5/sonnet-4/sonnet-4-thinking, codex: any model string, opencode: provider/model format)"`
	Repo              string `long:"repo" description:"Path to git repository (absolute or relative). If not provided, eksecd runs in no-repo mode with git operations disabled"`
	Version           bool   `long:"version" short:"v" description:"Show version information"`
}

func main() {
	var opts Options
	parser := flags.NewParser(&opts, flags.Default)

	_, err := parser.Parse()
	if err != nil {
		if flagsErr, ok := err.(*flags.Error); ok && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Handle version flag
	if opts.Version {
		fmt.Printf("%s\n", core.GetVersion())
		os.Exit(0)
	}

	// Always enable info level logging
	log.SetLevel(slog.LevelInfo)

	// Log startup information
	log.Info("🚀 eksecd starting - version %s", core.GetVersion())
	log.Info("⚙️  Configuration: agent=%s, permission_mode=%s", opts.Agent, func() string {
		if opts.BypassPermissions {
			return "bypassPermissions"
		}
		return "acceptEdits"
	}())
	if opts.Model != "" {
		log.Info("⚙️  Model: %s", opts.Model)
	}
	cwd, err := os.Getwd()
	if err == nil {
		log.Info("📁 Working directory: %s", cwd)
	}

	// Acquire directory lock to prevent multiple instances in same directory
	dirLock, err := utils.NewDirLock("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directory lock: %v\n", err)
		os.Exit(1)
	}

	if err := dirLock.TryLock(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Ensure lock is released on program exit
	defer func() {
		if unlockErr := dirLock.Unlock(); unlockErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to release directory lock: %v\n", unlockErr)
		}
	}()

	// Determine permission mode based on flag
	permissionMode := "acceptEdits"
	if opts.BypassPermissions {
		permissionMode = "bypassPermissions"
		fmt.Fprintf(
			os.Stderr,
			"Warning: --claude-bypass-permissions flag should only be used in a controlled, sandbox environment. Otherwise, anyone from Slack will have access to your entire system\n",
		)
	}

	// OpenCode only supports bypassPermissions mode
	if opts.Agent == "opencode" && permissionMode != "bypassPermissions" {
		fmt.Fprintf(
			os.Stderr,
			"Error: OpenCode only supports bypassPermissions mode. Use --claude-bypass-permissions flag.\n",
		)
		os.Exit(1)
	}

	cmdRunner, err := NewCmdRunner(opts.Agent, permissionMode, opts.Model, opts.Repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing CmdRunner: %v\n", err)
		os.Exit(1)
	}

	// Store locks in cmdRunner for cleanup
	cmdRunner.dirLock = dirLock

	// Setup program-wide logging from start
	logPath, err := cmdRunner.setupProgramLogging()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up program logging: %v\n", err)
		os.Exit(1)
	}
	log.Info("📝 Logging to: %s", logPath)

	// If in repo mode and repo path differs from cwd, acquire separate repository lock
	// (If repo path == cwd, the dirLock already covers it)
	repoCtx := cmdRunner.appState.GetRepositoryContext()
	if repoCtx.IsRepoMode && repoCtx.RepoPath != cwd {
		repoLock, err := utils.NewDirLock(repoCtx.RepoPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating repository lock: %v\n", err)
			os.Exit(1)
		}

		if err := repoLock.TryLock(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		cmdRunner.repoLock = repoLock
		log.Info("🔒 Acquired repository lock on %s", repoCtx.RepoPath)

		// Ensure repo lock is released on program exit
		defer func() {
			if unlockErr := repoLock.Unlock(); unlockErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to release repository lock: %v\n", unlockErr)
			}
		}()
	}

	// Validate Git environment and cleanup stale branches/worktrees (only if in repo mode)
	if repoCtx.IsRepoMode {
		err = cmdRunner.gitUseCase.ValidateGitEnvironment()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Git environment validation failed: %v\n", err)
			os.Exit(1)
		}

		// Cleanup orphaned worktrees first (must happen before branch cleanup)
		// Worktrees lock branches, so we must remove worktrees before deleting their branches
		err = cmdRunner.gitUseCase.CleanupOrphanedWorktrees()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to cleanup orphaned worktrees: %v\n", err)
			// Don't exit - this is not critical for agent operation
		}

		err = cmdRunner.gitUseCase.CleanupStaleBranches()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to cleanup stale branches: %v\n", err)
			// Don't exit - this is not critical for agent operation
		}
	}

	log.Info("🌐 WebSocket URL: %s", cmdRunner.wsURL)
	log.Info("🔑 Agent ID: %s", cmdRunner.agentID)

	// Start periodic cleanup routine (runs every 10 minutes) - only in repo mode
	if repoCtx.IsRepoMode {
		cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
		defer cleanupCancel()
		cmdRunner.startCleanupRoutine(cleanupCtx)
	}

	// Set up deferred cleanup
	defer func() {
		// Stop worktree pool first (before worker pools to ensure no new acquisitions)
		if cmdRunner.poolCancel != nil {
			cmdRunner.poolCancel()
		}
		if cmdRunner.gitUseCase.GetWorktreePool() != nil {
			cmdRunner.gitUseCase.GetWorktreePool().Stop()
			log.Info("🏊 Worktree pool stopped")
		}

		// Stop environment manager periodic refresh
		if cmdRunner.envManager != nil {
			cmdRunner.envManager.Stop()
		}

		// Close rotating writer to prevent file handle leak
		if cmdRunner.rotatingWriter != nil {
			if err := cmdRunner.rotatingWriter.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Failed to close log files: %v\n", err)
			}
		}

		if cmdRunner.rotatingWriter != nil {
			fmt.Fprintf(
				os.Stderr,
				"\n📝 App execution finished, logs for this session are in %s\n",
				cmdRunner.rotatingWriter.GetCurrentLogPath(),
			)
		}

		// Stop persistent worker pools on shutdown
		if cmdRunner.blockingWorkerPool != nil {
			cmdRunner.blockingWorkerPool.StopWait()
		}
		if cmdRunner.instantWorkerPool != nil {
			cmdRunner.instantWorkerPool.StopWait()
		}
	}()

	// Start Socket.IO client with backoff retry
	err = cmdRunner.startSocketIOClientWithRetry(cmdRunner.wsURL, cmdRunner.eksecAPIKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting WebSocket client after retries: %v\n", err)
		os.Exit(1)
	}
}

// startSocketIOClientWithRetry wraps startSocketIOClient with exponential backoff retry logic
func (cr *CmdRunner) startSocketIOClientWithRetry(serverURLStr, apiKey string) error {
	// Configure exponential backoff with unlimited retries
	expBackoff := backoff.NewExponentialBackOff()
	expBackoff.InitialInterval = 2 * time.Second
	expBackoff.MaxInterval = 10 * time.Second
	expBackoff.MaxElapsedTime = 0 // No time limit

	attempt := 0
	operation := func() error {
		attempt++
		log.Info("🔄 Connection attempt %d", attempt)

		err := cr.startSocketIOClient(serverURLStr, apiKey)
		if err != nil {
			log.Error("❌ Connection attempt %d failed: %v", attempt, err)
			return err
		}
		return nil
	}

	notify := func(err error, next time.Duration) {
		log.Info("⏳ Retrying in %v...", next)
	}

	err := backoff.RetryNotify(operation, expBackoff, notify)
	if err != nil {
		return fmt.Errorf("failed to connect after %d attempts: %w", attempt, err)
	}

	return nil
}

func (cr *CmdRunner) startSocketIOClient(serverURLStr, apiKey string) error {
	log.Info("📋 Starting to connect to Socket.IO server at %s", serverURLStr)

	// Set up global interrupt handling
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)

	// Set up Socket.IO client options
	opts := socket.DefaultOptions()
	opts.SetTransports(types.NewSet(socket.Polling, socket.WebSocket))

	// Disable automatic reconnection - handle reconnection externally with backoff
	opts.SetReconnection(false)

	// Get repository identifier from app state (set during git validation, or empty in no-repo mode)
	repoContext := cr.appState.GetRepositoryContext()
	repoIdentifier := repoContext.RepositoryIdentifier

	// Determine agent ID value - use env var if set, otherwise use repo identifier
	agentID := cr.envManager.Get("EKSEC_AGENT_ID")
	if agentID == "" {
		if repoIdentifier != "" {
			agentID = repoIdentifier
			log.Info("📋 Using repository identifier as agent ID: %s", agentID)
		} else {
			return fmt.Errorf("EKSEC_AGENT_ID environment variable is required in no-repo mode")
		}
	} else {
		log.Info("📋 Using EKSEC_AGENT_ID from environment: %s", agentID)
	}

	// Set authentication headers
	opts.SetExtraHeaders(map[string][]string{
		"X-CCAGENT-API-KEY": {apiKey},
		"X-CCAGENT-ID":      {cr.agentID},
		"X-CCAGENT-REPO":    {repoIdentifier},
		"X-AGENT-ID":        {agentID},
	})

	manager := socket.NewManager(serverURLStr, opts)
	socketClient := manager.Socket("/", opts)

	// Start MessageSender goroutine
	go cr.messageSender.Run(socketClient)
	log.Info("📤 Started MessageSender goroutine")

	// Use persistent worker pools across reconnects
	instantWorkerPool := cr.instantWorkerPool

	// Track connection state for auth failure detection
	connected := make(chan bool, 1)
	connectionError := make(chan error, 1)
	runtimeErrorChan := make(chan error, 1) // Errors after successful connection

	// Connection event handlers
	var err error
	err = socketClient.On("connect", func(args ...any) {
		log.Info("✅ Connected to Socket.IO server, socket ID: %s", socketClient.Id())
		cr.connectionState.SetConnected(true)
		connected <- true
	})
	utils.AssertInvariant(err == nil, fmt.Sprintf("Failed to set up connect handler: %v", err))

	err = socketClient.On("connect_error", func(args ...any) {
		log.Error("❌ Socket.IO connection error: %v", args)
		connectionError <- fmt.Errorf("socket.io connection error: %v", args)
	})
	utils.AssertInvariant(err == nil, fmt.Sprintf("Failed to set up connect_error handler: %v", err))

	err = socketClient.On("disconnect", func(args ...any) {
		log.Info("🔌 Socket.IO disconnected: %v", args)
		cr.connectionState.SetConnected(false)

		// Send disconnect error to trigger reconnection
		reason := "unknown"
		if len(args) > 0 {
			reason = fmt.Sprintf("%v", args[0])
		}

		select {
		case runtimeErrorChan <- fmt.Errorf("socket disconnected: %s", reason):
		default:
			// Channel full, ignore
		}
	})
	utils.AssertInvariant(err == nil, fmt.Sprintf("Failed to set up disconnect handler: %v", err))

	// Set up message handler for cc_message event
	err = socketClient.On("cc_message", func(data ...any) {
		if len(data) == 0 {
			log.Info("❌ No data received for cc_message event")
			return
		}

		var msg models.BaseMessage
		msgBytes, err := json.Marshal(data[0])
		if err != nil {
			log.Info("❌ Failed to marshal message data: %v", err)
			return
		}

		err = json.Unmarshal(msgBytes, &msg)
		if err != nil {
			log.Info("❌ Failed to unmarshal message data: %v", err)
			return
		}

		log.Info("📨 Received message type: %s", msg.Type)

		// Route messages to appropriate handler
		switch msg.Type {
		case models.MessageTypeStartConversation, models.MessageTypeUserMessage:
			// Persist message to queue BEFORE submitting for crash recovery
			if err := cr.messageHandler.PersistQueuedMessage(msg); err != nil {
				log.Error("❌ Failed to persist queued message: %v", err)
			}

			// Route through dispatcher for per-job sequential processing
			cr.dispatcher.Dispatch(msg)
		case models.MessageTypeCheckIdleJobs:
			// PR status checks can run in parallel without blocking conversations
			instantWorkerPool.Submit(func() {
				cr.messageHandler.HandleMessage(msg)
			})
		default:
			// Route other message types through dispatcher
			cr.dispatcher.Dispatch(msg)
		}
	})
	utils.AssertInvariant(err == nil, fmt.Sprintf("Failed to set up cc_message handler: %v", err))

	// Wait for initial connection or detect auth failure
	// Wait up to 10 seconds for initial connection
	select {
	case <-connected:
		log.Info("✅ Successfully authenticated with Socket.IO server")
	case err := <-connectionError:
		socketClient.Disconnect()
		return err
	case <-time.After(10 * time.Second):
		socketClient.Disconnect()
		return fmt.Errorf("connection timeout - server may have rejected authentication")
	}

	// Connection appears stable if not immediately disconnected within 5s (legacy guard removed)
	time.AfterFunc(5*time.Second, func() {
		log.Info("✅ Connection appears stable, continuing normal operation")
	})

	// Start ping routine once connected
	pingCtx, pingCancel := context.WithCancel(context.Background())
	defer pingCancel()
	cr.startPingRoutine(pingCtx, socketClient, runtimeErrorChan)

	// Wait for interrupt signal or runtime error
	select {
	case <-interrupt:
		log.Info("🔌 Interrupt received, closing Socket.IO connection...")
		socketClient.Disconnect()
		return nil
	case err := <-runtimeErrorChan:
		log.Error("❌ Runtime error occurred: %v", err)
		socketClient.Disconnect()
		return err
	}
}

func (cr *CmdRunner) setupProgramLogging() (string, error) {
	// Get config directory
	configDir, err := env.GetConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config directory: %w", err)
	}

	// Create logs directory
	logsDir := filepath.Join(configDir, "logs")

	// Set up rotating writer with 10MB file size limit
	rotatingWriter, err := log.NewRotatingWriter(log.RotatingWriterConfig{
		LogDir:      logsDir,
		MaxFileSize: 1024, // 10MB
		FilePrefix:  "eksecd",
		Stdout:      os.Stdout,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create rotating writer: %w", err)
	}

	// Store rotating writer reference for cleanup
	cr.rotatingWriter = rotatingWriter

	// Set the rotating writer as the log output
	log.SetWriter(rotatingWriter)

	return rotatingWriter.GetCurrentLogPath(), nil
}

func (cr *CmdRunner) startPingRoutine(ctx context.Context, socketClient *socket.Socket, runtimeErrorChan chan<- error) {
	log.Info("📋 Starting ping routine")
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info("📋 Ping routine stopped")
				return
			case <-ticker.C:
				// Check if socket is still connected
				if !socketClient.Connected() {
					log.Error("❌ Socket disconnected, stopping ping routine")
					select {
					case runtimeErrorChan <- fmt.Errorf("socket disconnected during ping"):
					default:
						// Channel full, ignore
					}
					return
				}

				log.Info("💓 Sending ping to server")
				if err := socketClient.Emit("ping"); err != nil {
					log.Error("❌ Failed to send ping: %v", err)
					select {
					case runtimeErrorChan <- fmt.Errorf("failed to send ping: %w", err):
					default:
						// Channel full, ignore
					}
					return
				}
			}
		}
	}()
}

func (cr *CmdRunner) startCleanupRoutine(ctx context.Context) {
	log.Info("🧹 Starting periodic cleanup routine (every 10 minutes)")
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info("🧹 Cleanup routine stopped")
				return
			case <-ticker.C:
				log.Info("🧹 Running periodic cleanup...")
				if err := cr.gitUseCase.CleanupOrphanedWorktrees(); err != nil {
					log.Warn("⚠️ Periodic worktree cleanup failed: %v", err)
				}
				if err := cr.gitUseCase.CleanupStaleBranches(); err != nil {
					log.Warn("⚠️ Periodic branch cleanup failed: %v", err)
				}
			}
		}
	}()
}
