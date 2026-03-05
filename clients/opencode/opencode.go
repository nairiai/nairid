package opencode

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"nairid/clients"
	"nairid/core"
	"nairid/core/log"
)

type OpenCodeClient struct {
	// No permissionsMode needed as we only support `bypassPermissions` for now.
}

func NewOpenCodeClient() *OpenCodeClient {
	return &OpenCodeClient{}
}

func (c *OpenCodeClient) StartNewSession(prompt string, options *clients.OpenCodeOptions) (string, error) {
	log.Info("📋 Starting to create new OpenCode session")

	args := []string{
		"run",
		"--format", "json",
		"--agent", "build", // Always use build mode until `acceptEdits` support is added
	}

	// Add model from options if provided
	if options != nil && options.Model != "" {
		args = append(args, "--model", options.Model)
	}

	// Append prompt as the last argument
	args = append(args, prompt)

	log.Info("Starting new OpenCode session with prompt: %s", prompt)
	log.Info("Command arguments: %v", args)

	ctx, cancel := context.WithTimeout(context.Background(), clients.DefaultSessionTimeout)
	defer cancel()

	var cmd = buildCommand(ctx, options, args)

	log.Info("Running OpenCode command (timeout: %s)", clients.DefaultSessionTimeout)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Error("⏰ OpenCode session timed out after %s", clients.DefaultSessionTimeout)
			return "", &core.ErrClaudeCommandErr{
				Err:    fmt.Errorf("session timed out after %s: %w", clients.DefaultSessionTimeout, err),
				Output: string(output),
			}
		}
		return "", &core.ErrClaudeCommandErr{
			Err:    err,
			Output: string(output),
		}
	}

	result := strings.TrimSpace(string(output))
	log.Info("OpenCode command completed successfully, outputLength: %d", len(result))
	log.Info("📋 Completed successfully - created new OpenCode session")
	return result, nil
}

func (c *OpenCodeClient) ContinueSession(sessionID, prompt string, options *clients.OpenCodeOptions) (string, error) {
	log.Info("📋 Starting to continue OpenCode session: %s", sessionID)

	args := []string{
		"run",
		"--session", sessionID,
		"--format", "json",
		"--agent", "build", // Always use build mode until `acceptEdits` support is added
	}

	// Add model from options if provided
	if options != nil && options.Model != "" {
		args = append(args, "--model", options.Model)
	}

	// Append prompt as the last argument
	args = append(args, prompt)

	log.Info("Executing OpenCode command with sessionID: %s, prompt: %s", sessionID, prompt)
	log.Info("Command arguments: %v", args)

	ctx, cancel := context.WithTimeout(context.Background(), clients.DefaultSessionTimeout)
	defer cancel()

	var cmd = buildCommand(ctx, options, args)

	log.Info("Running OpenCode command (timeout: %s)", clients.DefaultSessionTimeout)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Error("⏰ OpenCode session timed out after %s", clients.DefaultSessionTimeout)
			return "", &core.ErrClaudeCommandErr{
				Err:    fmt.Errorf("session timed out after %s: %w", clients.DefaultSessionTimeout, err),
				Output: string(output),
			}
		}
		return "", &core.ErrClaudeCommandErr{
			Err:    err,
			Output: string(output),
		}
	}

	result := strings.TrimSpace(string(output))
	log.Info("OpenCode command completed successfully, outputLength: %d", len(result))
	log.Info("📋 Completed successfully - continued OpenCode session")
	return result, nil
}

// buildCommand creates the appropriate exec.Cmd with context based on options
func buildCommand(ctx context.Context, options *clients.OpenCodeOptions, args []string) *exec.Cmd {
	if options != nil && options.WorkDir != "" {
		log.Info("Using working directory: %s", options.WorkDir)
		return clients.BuildAgentCommandWithContextAndWorkDir(ctx, options.WorkDir, "opencode", args...)
	}
	return clients.BuildAgentCommandWithContext(ctx, "opencode", args...)
}
