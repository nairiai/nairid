package cursor

import (
	"context"
	"encoding/json"
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

	log.Info("Cursor command: %s", clients.ArgsForLog(args, finalPrompt))

	timeout := options.RunTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := clients.BuildAgentCommandWithContext(ctx, "cursor-agent", args...)

	log.Info("Running Cursor command (timeout: %s)", timeout)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Error("⏰ Cursor session timed out after %s", timeout)
			return "", &core.ErrClaudeCommandErr{
				Agent:  "Cursor",
				Err:    &core.ErrSessionTimeout{Timeout: timeout, Err: err},
				Output: withoutPromptEcho(output),
			}
		}
		return "", &core.ErrClaudeCommandErr{
			Agent:  "Cursor",
			Err:    err,
			Output: withoutPromptEcho(output),
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

	log.Info("Cursor command: %s", clients.ArgsForLog(args, prompt))

	timeout := options.RunTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := clients.BuildAgentCommandWithContext(ctx, "cursor-agent", args...)

	log.Info("Running Cursor command (timeout: %s)", timeout)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Error("⏰ Cursor session timed out after %s", timeout)
			return "", &core.ErrClaudeCommandErr{
				Agent:  "Cursor",
				Err:    &core.ErrSessionTimeout{Timeout: timeout, Err: err},
				Output: withoutPromptEcho(output),
			}
		}
		return "", &core.ErrClaudeCommandErr{
			Agent:  "Cursor",
			Err:    err,
			Output: withoutPromptEcho(output),
		}
	}

	result := strings.TrimSpace(string(output))
	log.Info("Cursor command completed successfully, outputLength: %d", len(result))
	log.Info("📋 Completed successfully - continued Cursor session")
	return result, nil
}

// withoutPromptEcho drops the stream-json events cursor-agent prints for the user's own message,
// so an error that carries the CLI's output does not carry the prompt into the logs.
func withoutPromptEcho(output []byte) string {
	lines := strings.Split(string(output), "\n")
	kept := lines[:0]
	for _, line := range lines {
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && event.Type == "user" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
