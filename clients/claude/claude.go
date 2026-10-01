package claude

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

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
		"-p",
	)
	promptArgs, promptStdin := clients.ApplyPrompt(prompt)
	args = append(args, promptArgs...)

	args = appendOptionArgs(args, options)

	log.Info("Starting new Claude session with prompt: %s", prompt)
	log.Info("Command arguments: %v", args)

	timeout := clients.SessionTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := c.buildCommand(ctx, options, args)
	if promptStdin != nil {
		cmd.Stdin = promptStdin
	}

	log.Info("Running Claude command (timeout: %s)", timeout)
	result, err := clients.RunCommandStreaming(ctx, cmd, onLine)
	if err != nil {
		return "", handleCommandError(ctx, err, "Claude", timeout)
	}

	log.Info("Claude command completed successfully, outputLength: %d", len(result))
	log.Info("📋 Completed successfully - created new Claude session")
	return result, nil
}

// appendOptionArgs adds the per-session flags shared by new and resumed sessions.
func appendOptionArgs(args []string, options *clients.ClaudeOptions) []string {
	if options == nil {
		return args
	}
	if options.Model != "" {
		args = append(args, "--model", options.Model)
	}
	if options.Effort != "" {
		args = append(args, "--effort", options.Effort)
	}
	if options.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", options.SystemPrompt)
	}
	if len(options.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(options.DisallowedTools, " "))
	}
	return args
}

func (c *ClaudeClient) ContinueSession(sessionID, prompt string, options *clients.ClaudeOptions, onLine clients.ProgressCallback) (string, error) {
	log.Info("📋 Starting to continue Claude session: %s", sessionID)
	args := c.buildPermissionArgs()
	args = append(args,
		"--verbose",
		"--output-format", "stream-json",
		"--resume", sessionID,
		"-p",
	)
	promptArgs, promptStdin := clients.ApplyPrompt(prompt)
	args = append(args, promptArgs...)

	args = appendOptionArgs(args, options)

	log.Info("Executing Claude command with sessionID: %s, prompt: %s", sessionID, prompt)
	log.Info("Command arguments: %v", args)

	timeout := clients.SessionTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := c.buildCommand(ctx, options, args)
	if promptStdin != nil {
		cmd.Stdin = promptStdin
	}

	log.Info("Running Claude command (timeout: %s)", timeout)
	result, err := clients.RunCommandStreaming(ctx, cmd, onLine)
	if err != nil {
		return "", handleCommandError(ctx, err, "Claude", timeout)
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

// buildCommand creates the appropriate exec.Cmd with context based on options
func (c *ClaudeClient) buildCommand(ctx context.Context, options *clients.ClaudeOptions, args []string) *exec.Cmd {
	if options != nil && options.WorkDir != "" {
		log.Info("Using working directory: %s", options.WorkDir)
		return clients.BuildAgentCommandWithContextAndWorkDir(ctx, options.WorkDir, "claude", args...)
	}
	return clients.BuildAgentCommandWithContext(ctx, "claude", args...)
}
