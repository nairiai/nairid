package codex

import (
	"context"
	"errors"
	"fmt"
	"time"

	"nairid/clients"
	"nairid/core"
	"nairid/core/log"
)

type CodexClient struct {
	permissionMode string
	workDir        string
}

func NewCodexClient(permissionMode, workDir string) *CodexClient {
	return &CodexClient{
		permissionMode: permissionMode,
		workDir:        workDir,
	}
}

func (c *CodexClient) StartNewSession(prompt string, options *clients.CodexOptions, onLine clients.ProgressCallback) (string, error) {
	log.Info("📋 Starting to create new Codex session")

	args := c.buildBaseArgs(options)
	args = append(args, prompt)

	log.Info("Starting new Codex session with prompt: %s", prompt)
	log.Info("Command arguments: %v", args)

	timeout := clients.SessionTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := clients.BuildAgentCommandWithContext(ctx, "codex", args...)
	if c.workDir != "" {
		cmd.Dir = c.workDir
	}

	log.Info("Running Codex command (timeout: %s)", timeout)
	result, err := clients.RunCommandStreaming(ctx, cmd, onLine)
	if err != nil {
		return "", handleCommandError(ctx, err, "Codex", timeout)
	}

	log.Info("Codex command completed successfully, outputLength: %d", len(result))
	log.Info("📋 Completed successfully - created new Codex session")
	return result, nil
}

func (c *CodexClient) ContinueSession(threadID, prompt string, options *clients.CodexOptions, onLine clients.ProgressCallback) (string, error) {
	log.Info("📋 Starting to continue Codex session: %s", threadID)

	// Command structure: codex [GLOBAL_OPTIONS] exec [EXEC_OPTIONS] resume [SESSION_ID] [PROMPT]
	args := c.buildBaseArgs(options)

	// RESUME SUBCOMMAND with session ID and prompt
	args = append(args, "resume", threadID, prompt)

	log.Info("Executing Codex command with threadID: %s, prompt: %s", threadID, prompt)
	log.Info("Command arguments: %v", args)

	timeout := clients.SessionTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := clients.BuildAgentCommandWithContext(ctx, "codex", args...)
	if c.workDir != "" {
		cmd.Dir = c.workDir
	}

	log.Info("Running Codex command (timeout: %s)", timeout)
	result, err := clients.RunCommandStreaming(ctx, cmd, onLine)
	if err != nil {
		return "", handleCommandError(ctx, err, "Codex", timeout)
	}

	log.Info("Codex command completed successfully, outputLength: %d", len(result))
	log.Info("📋 Completed successfully - continued Codex session")
	return result, nil
}

// handleCommandError converts a clients.CommandError to a core.ErrClaudeCommandErr,
// including timeout detection via context deadline.
func handleCommandError(ctx context.Context, err error, agentName string, timeout time.Duration) error {
	var cmdErr *clients.CommandError
	if errors.As(err, &cmdErr) {
		if ctx.Err() == context.DeadlineExceeded {
			log.Error("⏰ %s session timed out after %s", agentName, timeout)
			return &core.ErrClaudeCommandErr{
				Err:    fmt.Errorf("session timed out after %s: %w", timeout, cmdErr.Err),
				Output: cmdErr.Output,
			}
		}
		return &core.ErrClaudeCommandErr{
			Err:    cmdErr.Err,
			Output: cmdErr.Output,
		}
	}
	return err
}

// buildBaseArgs constructs the base command arguments for Codex sessions (both new and resume)
// Command structure: codex [GLOBAL_OPTIONS] exec [EXEC_OPTIONS]
func (c *CodexClient) buildBaseArgs(options *clients.CodexOptions) []string {
	var args []string

	// GLOBAL OPTIONS (before 'exec' subcommand)

	// Working directory
	if c.workDir != "" {
		args = append(args, "-C", c.workDir)
	}

	// Model selection
	if options != nil && options.Model != "" {
		args = append(args, "-m", options.Model)
	}
	if options != nil && options.Effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+options.Effort)
	}

	// Web search - always enabled
	args = append(args, "--search")

	// EXEC SUBCOMMAND
	args = append(args, "exec")

	// EXEC OPTIONS (after 'exec' subcommand)

	// Permission mode - map nairid modes to Codex flags
	if c.permissionMode == "bypassPermissions" {
		// Completely unrestricted access (no sandbox, no approvals)
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	} else {
		// Default: workspace writes allowed
		if options != nil && options.Sandbox != "" {
			// Use custom sandbox mode if provided
			args = append(args, "--sandbox", options.Sandbox)
		} else {
			// Default sandbox mode
			args = append(args, "--sandbox", "workspace-write")
		}
	}

	args = append(args, "--json", "--skip-git-repo-check")

	return args
}
