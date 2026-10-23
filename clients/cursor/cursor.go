package cursor

import (
	"context"
	"fmt"
	"strings"

	"nairid/clients"
	"nairid/core"
	"nairid/core/log"
)

type CursorClient struct {
	// No permissionMode needed for cursor-agent as it handles permissions differently
}

func NewCursorClient() *CursorClient {
	return &CursorClient{}
}

func (c *CursorClient) StartNewSession(ctx context.Context, prompt string, options *clients.CursorOptions) (string, error) {
	log.Info("📋 Starting to create new Cursor session")

	// Prepend system prompt if provided in options
	finalPrompt := prompt
	if options != nil && options.SystemPrompt != "" {
		finalPrompt = "# BEHAVIOR INSTRUCTIONS\n" +
			options.SystemPrompt + "\n\n" +
			"# USER MESSAGE\n" +
			prompt
		log.Info("Prepending system prompt to user prompt with clear delimiters")
	}

	args := []string{
		"--force", // otherwise, it will wait for approval for all mutation commands
		"--print",
		"--output-format", "stream-json",
		finalPrompt,
	}

	// Add model from options if provided
	if options != nil && options.Model != "" {
		args = append([]string{"--model", options.Model}, args...)
	}

	log.Info("Starting new Cursor session with prompt: %s", finalPrompt)
	log.Info("Command arguments: %v", args)

	timeout := clients.SessionTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := clients.BuildAgentCommandWithContext(ctx, "cursor-agent", args...)

	log.Info("Running Cursor command (timeout: %s)", timeout)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Error("⏰ Cursor session timed out after %s", timeout)
			return "", &core.ErrClaudeCommandErr{
				Err:    fmt.Errorf("session timed out after %s: %w", timeout, err),
				Output: string(output),
			}
		}
		return "", &core.ErrClaudeCommandErr{
			Err:    err,
			Output: string(output),
		}
	}

	result := strings.TrimSpace(string(output))
	log.Info("Cursor command completed successfully, outputLength: %d", len(result))
	log.Info("📋 Completed successfully - created new Cursor session")
	return result, nil
}

func (c *CursorClient) ContinueSession(ctx context.Context, sessionID, prompt string, options *clients.CursorOptions) (string, error) {
	log.Info("📋 Starting to continue Cursor session: %s", sessionID)
	args := []string{
		"--force", // otherwise, it will wait for approval for all mutation commands
		"--print",
		"--output-format", "stream-json",
		"--resume", sessionID,
		prompt,
	}

	// Add model from options if provided
	if options != nil && options.Model != "" {
		args = append([]string{"--model", options.Model}, args...)
	}

	log.Info("Executing Cursor command with sessionID: %s, prompt: %s", sessionID, prompt)
	log.Info("Command arguments: %v", args)

	timeout := clients.SessionTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := clients.BuildAgentCommandWithContext(ctx, "cursor-agent", args...)

	log.Info("Running Cursor command (timeout: %s)", timeout)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Error("⏰ Cursor session timed out after %s", timeout)
			return "", &core.ErrClaudeCommandErr{
				Err:    fmt.Errorf("session timed out after %s: %w", timeout, err),
				Output: string(output),
			}
		}
		return "", &core.ErrClaudeCommandErr{
			Err:    err,
			Output: string(output),
		}
	}

	result := strings.TrimSpace(string(output))
	log.Info("Cursor command completed successfully, outputLength: %d", len(result))
	log.Info("📋 Completed successfully - continued Cursor session")
	return result, nil
}
