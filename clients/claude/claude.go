package claude

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"nairid/clients"
	"nairid/core"
	"nairid/core/log"
)

type ClaudeClient struct {
	permissionMode string
}

func NewClaudeClient(permissionMode string) *ClaudeClient {
	return &ClaudeClient{
		permissionMode: permissionMode,
	}
}

func (c *ClaudeClient) StartNewSession(prompt string, options *clients.ClaudeOptions, onLine clients.ProgressCallback) (string, error) {
	log.Info("📋 Starting to create new Claude session")
	args := c.buildPermissionArgs()
	args = append(args,
		"--verbose",
		"--output-format", "stream-json",
		"-p", prompt,
	)

	if options != nil {
		if options.Model != "" {
			args = append(args, "--model", options.Model)
		}
		if options.SystemPrompt != "" {
			args = append(args, "--append-system-prompt", options.SystemPrompt)
		}
		if len(options.DisallowedTools) > 0 {
			disallowedToolsStr := strings.Join(options.DisallowedTools, " ")
			args = append(args, "--disallowedTools", disallowedToolsStr)
		}
	}

	log.Info("Starting new Claude session with prompt: %s", prompt)
	log.Info("Command arguments: %v", args)

	ctx, cancel := context.WithTimeout(context.Background(), clients.DefaultSessionTimeout)
	defer cancel()

	cmd := c.buildCommand(ctx, options, args)

	log.Info("Running Claude command (timeout: %s)", clients.DefaultSessionTimeout)
	result, err := clients.RunCommandStreaming(ctx, cmd, onLine)
	if err != nil {
		return "", handleCommandError(ctx, err, "Claude")
	}

	log.Info("Claude command completed successfully, outputLength: %d", len(result))
	log.Info("📋 Completed successfully - created new Claude session")
	return result, nil
}

func (c *ClaudeClient) ContinueSession(sessionID, prompt string, options *clients.ClaudeOptions, onLine clients.ProgressCallback) (string, error) {
	log.Info("📋 Starting to continue Claude session: %s", sessionID)
	args := c.buildPermissionArgs()
	args = append(args,
		"--verbose",
		"--output-format", "stream-json",
		"--resume", sessionID,
		"-p", prompt,
	)

	if options != nil {
		if options.Model != "" {
			args = append(args, "--model", options.Model)
		}
		if options.SystemPrompt != "" {
			args = append(args, "--append-system-prompt", options.SystemPrompt)
		}
		if len(options.DisallowedTools) > 0 {
			disallowedToolsStr := strings.Join(options.DisallowedTools, " ")
			args = append(args, "--disallowedTools", disallowedToolsStr)
		}
	}

	log.Info("Executing Claude command with sessionID: %s, prompt: %s", sessionID, prompt)
	log.Info("Command arguments: %v", args)

	ctx, cancel := context.WithTimeout(context.Background(), clients.DefaultSessionTimeout)
	defer cancel()

	cmd := c.buildCommand(ctx, options, args)

	log.Info("Running Claude command (timeout: %s)", clients.DefaultSessionTimeout)
	result, err := clients.RunCommandStreaming(ctx, cmd, onLine)
	if err != nil {
		return "", handleCommandError(ctx, err, "Claude")
	}

	log.Info("Claude command completed successfully, outputLength: %d", len(result))
	log.Info("📋 Completed successfully - continued Claude session")
	return result, nil
}

// buildPermissionArgs returns the CLI args for the configured permission mode.
// When bypassPermissions is set, we use --dangerously-skip-permissions instead of
// --permission-mode bypassPermissions because the latter causes Claude Code to
// revert file edits on session exit.
func (c *ClaudeClient) buildPermissionArgs() []string {
	if c.permissionMode == "bypassPermissions" {
		return []string{"--dangerously-skip-permissions"}
	}
	return []string{"--permission-mode", c.permissionMode}
}

// handleCommandError converts a clients.CommandError to a core.ErrClaudeCommandErr,
// including timeout detection via context deadline.
func handleCommandError(ctx context.Context, err error, agentName string) error {
	var cmdErr *clients.CommandError
	if errors.As(err, &cmdErr) {
		if ctx.Err() == context.DeadlineExceeded {
			log.Error("⏰ %s session timed out after %s", agentName, clients.DefaultSessionTimeout)
			return &core.ErrClaudeCommandErr{
				Err:    fmt.Errorf("session timed out after %s: %w", clients.DefaultSessionTimeout, cmdErr.Err),
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

// buildCommand creates the appropriate exec.Cmd with context based on options
func (c *ClaudeClient) buildCommand(ctx context.Context, options *clients.ClaudeOptions, args []string) *exec.Cmd {
	if options != nil && options.WorkDir != "" {
		log.Info("Using working directory: %s", options.WorkDir)
		return clients.BuildAgentCommandWithContextAndWorkDir(ctx, options.WorkDir, "claude", args...)
	}
	return clients.BuildAgentCommandWithContext(ctx, "claude", args...)
}
