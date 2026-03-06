package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nairid/clients"
	"nairid/core"
	"nairid/core/log"
	"nairid/services"
)

type CodexService struct {
	codexClient clients.CodexClient
	logDir      string
	model       string
}

func NewCodexService(codexClient clients.CodexClient, logDir, model string) *CodexService {
	return &CodexService{
		codexClient: codexClient,
		logDir:      logDir,
		model:       model,
	}
}

// writeCodexSessionLog writes Codex output to a timestamped log file and returns the filepath
func (c *CodexService) writeCodexSessionLog(rawOutput string) (string, error) {
	if err := os.MkdirAll(c.logDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create log directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("codex-session-%s.log", timestamp)
	filepath := filepath.Join(c.logDir, filename)

	if err := os.WriteFile(filepath, []byte(rawOutput), 0600); err != nil {
		return "", fmt.Errorf("failed to write log file: %w", err)
	}

	return filepath, nil
}

// CleanupOldLogs removes log files older than the specified number of days
func (c *CodexService) CleanupOldLogs(maxAgeDays int) error {
	log.Info("📋 Starting to cleanup old Codex session logs older than %d days", maxAgeDays)

	if maxAgeDays <= 0 {
		return fmt.Errorf("maxAgeDays must be greater than 0")
	}

	files, err := os.ReadDir(c.logDir)
	if err != nil {
		if os.IsNotExist(err) {
			log.Info("📋 Log directory does not exist, nothing to clean up")
			return nil
		}
		return fmt.Errorf("failed to read log directory: %w", err)
	}

	cutoffTime := time.Now().AddDate(0, 0, -maxAgeDays)
	removedCount := 0

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// Only clean up codex session log files
		if !strings.HasPrefix(file.Name(), "codex-session-") || !strings.HasSuffix(file.Name(), ".log") {
			continue
		}

		filePath := filepath.Join(c.logDir, file.Name())
		info, err := file.Info()
		if err != nil {
			log.Error("Failed to get file info for %s: %v", filePath, err)
			continue
		}

		if info.ModTime().Before(cutoffTime) {
			if err := os.Remove(filePath); err != nil {
				log.Error("Failed to remove old log file %s: %v", filePath, err)
				continue
			}
			removedCount++
		}
	}

	log.Info("📋 Completed successfully - removed %d old Codex session log files", removedCount)
	return nil
}

func (c *CodexService) StartNewConversation(prompt string) (*services.CLIAgentResult, error) {
	return c.StartNewConversationWithOptions(prompt, nil)
}

// deriveCodexOptions creates a final options struct, applying service model if set
func (c *CodexService) deriveCodexOptions(options *clients.CodexOptions) *clients.CodexOptions {
	finalOptions := options
	if c.model != "" {
		if finalOptions == nil {
			finalOptions = &clients.CodexOptions{Model: c.model}
		} else {
			// Create a copy to avoid modifying the original
			finalOptions = &clients.CodexOptions{
				Model:     c.model, // Service model takes precedence
				Sandbox:   finalOptions.Sandbox,
				WebSearch: finalOptions.WebSearch,
			}
		}
	}
	return finalOptions
}

func (c *CodexService) StartNewConversationWithOptions(
	prompt string,
	options *clients.CodexOptions,
) (*services.CLIAgentResult, error) {
	log.Info("📋 Starting to start new Codex conversation")

	finalOptions := c.deriveCodexOptions(options)

	rawOutput, err := c.codexClient.StartNewSession(prompt, finalOptions)
	if err != nil {
		log.Error("Failed to start new Codex session: %v", err)
		return nil, c.handleCodexClientError(err, "failed to start new Codex session")
	}

	// Always log the Codex session
	logPath, writeErr := c.writeCodexSessionLog(rawOutput)
	if writeErr != nil {
		log.Error("Failed to write Codex session log: %v", writeErr)
	}

	messages, err := MapCodexOutputToMessages(rawOutput)
	if err != nil {
		log.Error("Failed to parse Codex output: %v", err)

		return nil, &core.ClaudeParseError{ // Reusing Claude parse error for consistency
			Message:     fmt.Sprintf("couldn't parse codex response and instead stored the response in %s", logPath),
			LogFilePath: logPath,
			OriginalErr: err,
		}
	}

	threadID := ExtractCodexThreadID(messages)
	output, err := ExtractCodexResult(messages)
	if err != nil {
		log.Error("Failed to extract Codex result: %v", err)
		return nil, fmt.Errorf("failed to extract Codex result: %w", err)
	}

	log.Info("📋 Codex response extracted successfully, thread: %s, output length: %d", threadID, len(output))
	result := &services.CLIAgentResult{
		Output:    output,
		SessionID: threadID,
	}

	log.Info("📋 Completed successfully - started new Codex conversation with thread: %s", threadID)
	return result, nil
}

func (c *CodexService) StartNewConversationWithSystemPrompt(
	prompt, systemPrompt string,
) (*services.CLIAgentResult, error) {
	// Codex doesn't have a system prompt option like Claude
	// We could prepend it to the prompt similar to Cursor's approach
	finalPrompt := "# BEHAVIOR INSTRUCTIONS\n" +
		systemPrompt + "\n\n" +
		"# USER MESSAGE\n" +
		prompt
	log.Info("Prepending system prompt to user prompt with clear delimiters")
	return c.StartNewConversationWithOptions(finalPrompt, nil)
}

func (c *CodexService) StartNewConversationWithDisallowedTools(
	prompt string,
	disallowedTools []string,
) (*services.CLIAgentResult, error) {
	// Codex doesn't have a disallowed tools option
	// Return the conversation without this feature
	return c.StartNewConversationWithOptions(prompt, nil)
}

func (c *CodexService) ContinueConversation(sessionID, prompt string) (*services.CLIAgentResult, error) {
	return c.ContinueConversationWithOptions(sessionID, prompt, nil)
}

func (c *CodexService) ContinueConversationWithSystemPrompt(sessionID, prompt, systemPrompt string) (*services.CLIAgentResult, error) {
	// System prompt persists from turn 1 (prepended into user message), no action needed on continue
	return c.ContinueConversation(sessionID, prompt)
}

// StartNewConversationInDir starts a new conversation in a specific working directory
// Note: Codex does not support custom working directories yet, falls back to default behavior
func (c *CodexService) StartNewConversationInDir(prompt, workDir string) (*services.CLIAgentResult, error) {
	log.Warn("⚠️ Codex does not support custom working directories, ignoring workDir: %s", workDir)
	return c.StartNewConversation(prompt)
}

// StartNewConversationWithSystemPromptInDir starts a new conversation with system prompt in a specific directory
// Note: Codex does not support custom working directories yet, falls back to default behavior
func (c *CodexService) StartNewConversationWithSystemPromptInDir(
	prompt, systemPrompt, workDir string,
) (*services.CLIAgentResult, error) {
	log.Warn("⚠️ Codex does not support custom working directories, ignoring workDir: %s", workDir)
	return c.StartNewConversationWithSystemPrompt(prompt, systemPrompt)
}

func (c *CodexService) ContinueConversationWithSystemPromptInDir(sessionID, prompt, systemPrompt, workDir string) (*services.CLIAgentResult, error) {
	// System prompt persists from turn 1 (prepended into user message), no action needed on continue
	return c.ContinueConversationInDir(sessionID, prompt, workDir)
}

// ContinueConversationInDir continues an existing conversation in a specific directory
// Note: Codex does not support custom working directories yet, falls back to default behavior
func (c *CodexService) ContinueConversationInDir(sessionID, prompt, workDir string) (*services.CLIAgentResult, error) {
	log.Warn("⚠️ Codex does not support custom working directories, ignoring workDir: %s", workDir)
	return c.ContinueConversation(sessionID, prompt)
}

func (c *CodexService) ContinueConversationWithOptions(
	sessionID, prompt string,
	options *clients.CodexOptions,
) (*services.CLIAgentResult, error) {
	log.Info("📋 Starting to continue Codex conversation: %s", sessionID)

	finalOptions := c.deriveCodexOptions(options)

	rawOutput, err := c.codexClient.ContinueSession(sessionID, prompt, finalOptions)
	if err != nil {
		log.Error("Failed to continue Codex session: %v", err)
		return nil, c.handleCodexClientError(err, "failed to continue Codex session")
	}

	// Always log the Codex session
	logPath, writeErr := c.writeCodexSessionLog(rawOutput)
	if writeErr != nil {
		log.Error("Failed to write Codex session log: %v", writeErr)
	}

	messages, err := MapCodexOutputToMessages(rawOutput)
	if err != nil {
		log.Error("Failed to parse Codex output: %v", err)

		return nil, &core.ClaudeParseError{
			Message:     fmt.Sprintf("couldn't parse codex response and instead stored the response in %s", logPath),
			LogFilePath: logPath,
			OriginalErr: err,
		}
	}

	actualThreadID := ExtractCodexThreadID(messages)
	output, err := ExtractCodexResult(messages)
	if err != nil {
		log.Error("Failed to extract Codex result: %v", err)
		return nil, fmt.Errorf("failed to extract Codex result: %w", err)
	}

	log.Info("📋 Codex response extracted successfully, thread: %s, output length: %d", actualThreadID, len(output))
	result := &services.CLIAgentResult{
		Output:    output,
		SessionID: actualThreadID,
	}

	log.Info("📋 Completed successfully - continued Codex conversation with thread: %s", actualThreadID)
	return result, nil
}

// handleCodexClientError processes errors from Codex client calls.
func (c *CodexService) handleCodexClientError(err error, operation string) error {
	if err == nil {
		return nil
	}

	// Check if this is a Codex command error (reusing Claude error type)
	claudeErr, isClaudeErr := core.IsClaudeCommandErr(err)
	if !isClaudeErr {
		// Not a command error, return original error wrapped
		return fmt.Errorf("%s: %w", operation, err)
	}

	// Try to parse the output as Codex messages using internal parsing
	messages, parseErr := MapCodexOutputToMessages(claudeErr.Output)
	if parseErr != nil {
		// If parsing fails, return original error wrapped
		log.Error("Failed to parse Codex output from error: %v", parseErr)
		return fmt.Errorf("%s: %w", operation, err)
	}

	// Try to extract the agent message even from errors
	output, extractErr := ExtractCodexResult(messages)
	if extractErr == nil && output != "" {
		log.Info("✅ Successfully extracted Codex agent message from error: %s", output)
		return fmt.Errorf("%s: %s", operation, output)
	}

	// No agent message found, return original error wrapped
	log.Info("⚠️ No agent message found in Codex command output, returning original error")
	return fmt.Errorf("%s: %w", operation, err)
}

// AgentName identifies this service implementation
func (c *CodexService) AgentName() string {
	return "codex"
}

