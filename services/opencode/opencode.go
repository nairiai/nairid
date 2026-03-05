package opencode

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

type OpenCodeService struct {
	openCodeClient clients.OpenCodeClient
	logDir         string
	model          string
}

func NewOpenCodeService(openCodeClient clients.OpenCodeClient, logDir, model string) *OpenCodeService {
	return &OpenCodeService{
		openCodeClient: openCodeClient,
		logDir:         logDir,
		model:          model,
	}
}

// writeOpenCodeSessionLog writes OpenCode output to a timestamped log file and returns the filepath
func (o *OpenCodeService) writeOpenCodeSessionLog(rawOutput string) (string, error) {
	if err := os.MkdirAll(o.logDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create log directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("opencode-session-%s.log", timestamp)
	filepath := filepath.Join(o.logDir, filename)

	if err := os.WriteFile(filepath, []byte(rawOutput), 0600); err != nil {
		return "", fmt.Errorf("failed to write log file: %w", err)
	}

	return filepath, nil
}

// CleanupOldLogs removes log files older than the specified number of days
func (o *OpenCodeService) CleanupOldLogs(maxAgeDays int) error {
	log.Info("📋 Starting to cleanup old OpenCode session logs older than %d days", maxAgeDays)

	if maxAgeDays <= 0 {
		return fmt.Errorf("maxAgeDays must be greater than 0")
	}

	files, err := os.ReadDir(o.logDir)
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

		// Only clean up opencode session log files
		if !strings.HasPrefix(file.Name(), "opencode-session-") || !strings.HasSuffix(file.Name(), ".log") {
			continue
		}

		filePath := filepath.Join(o.logDir, file.Name())
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

	log.Info("📋 Completed successfully - removed %d old OpenCode session log files", removedCount)
	return nil
}

func (o *OpenCodeService) StartNewConversation(prompt string) (*services.CLIAgentResult, error) {
	return o.StartNewConversationWithOptions(prompt, nil)
}

// deriveOpenCodeOptions creates a final options struct, applying service model if set
func (o *OpenCodeService) deriveOpenCodeOptions(options *clients.OpenCodeOptions) *clients.OpenCodeOptions {
	if options == nil {
		if o.model != "" {
			return &clients.OpenCodeOptions{Model: o.model}
		}
		return nil
	}

	// Create a copy to avoid modifying the original, preserving WorkDir
	finalOptions := &clients.OpenCodeOptions{
		WorkDir: options.WorkDir,
	}

	// Apply service model if set, otherwise use options model
	if o.model != "" {
		finalOptions.Model = o.model
	} else {
		finalOptions.Model = options.Model
	}

	return finalOptions
}

func (o *OpenCodeService) StartNewConversationWithOptions(
	prompt string,
	options *clients.OpenCodeOptions,
) (*services.CLIAgentResult, error) {
	log.Info("📋 Starting to start new OpenCode conversation")

	finalOptions := o.deriveOpenCodeOptions(options)

	rawOutput, err := o.openCodeClient.StartNewSession(prompt, finalOptions)
	if err != nil {
		log.Error("Failed to start new OpenCode session: %v", err)
		return nil, o.handleOpenCodeClientError(err, "failed to start new OpenCode session")
	}

	// Always log the OpenCode session
	logPath, writeErr := o.writeOpenCodeSessionLog(rawOutput)
	if writeErr != nil {
		log.Error("Failed to write OpenCode session log: %v", writeErr)
	}

	messages, err := MapOpenCodeOutputToMessages(rawOutput)
	if err != nil {
		log.Error("Failed to parse OpenCode output: %v", err)

		return nil, &core.ClaudeParseError{ // Reusing Claude parse error for consistency
			Message:     fmt.Sprintf("couldn't parse opencode response and instead stored the response in %s", logPath),
			LogFilePath: logPath,
			OriginalErr: err,
		}
	}

	sessionID := ExtractOpenCodeSessionID(messages)
	output, err := ExtractOpenCodeResult(messages)
	if err != nil {
		log.Error("Failed to extract OpenCode result: %v", err)
		return nil, fmt.Errorf("failed to extract OpenCode result: %w", err)
	}

	log.Info("📋 OpenCode response extracted successfully, session: %s, output length: %d", sessionID, len(output))
	result := &services.CLIAgentResult{
		Output:    output,
		SessionID: sessionID,
	}

	log.Info("📋 Completed successfully - started new OpenCode conversation with session: %s", sessionID)
	return result, nil
}

func (o *OpenCodeService) StartNewConversationWithSystemPrompt(
	prompt, systemPrompt string,
) (*services.CLIAgentResult, error) {
	// OpenCode doesn't have a system prompt option like Claude
	// We prepend it to the prompt similar to Cursor's approach
	finalPrompt := "# BEHAVIOR INSTRUCTIONS\n" +
		systemPrompt + "\n\n" +
		"# USER MESSAGE\n" +
		prompt
	log.Info("Prepending system prompt to user prompt with clear delimiters")
	return o.StartNewConversationWithOptions(finalPrompt, nil)
}

func (o *OpenCodeService) StartNewConversationWithDisallowedTools(
	prompt string,
	disallowedTools []string,
) (*services.CLIAgentResult, error) {
	// OpenCode doesn't have a disallowed tools option via CLI
	log.Info("⚠️ OpenCode doesn't support disallowed tools via CLI")
	return o.StartNewConversationWithOptions(prompt, nil)
}

func (o *OpenCodeService) ContinueConversation(sessionID, prompt string) (*services.CLIAgentResult, error) {
	return o.ContinueConversationWithOptions(sessionID, prompt, nil)
}

// StartNewConversationInDir starts a new conversation in a specific working directory
func (o *OpenCodeService) StartNewConversationInDir(prompt, workDir string) (*services.CLIAgentResult, error) {
	return o.StartNewConversationWithOptions(prompt, &clients.OpenCodeOptions{
		WorkDir: workDir,
	})
}

// StartNewConversationWithSystemPromptInDir starts a new conversation with system prompt in a specific directory
func (o *OpenCodeService) StartNewConversationWithSystemPromptInDir(
	prompt, systemPrompt, workDir string,
) (*services.CLIAgentResult, error) {
	// OpenCode doesn't have a system prompt option like Claude
	// We prepend it to the prompt similar to Cursor's approach
	finalPrompt := "# BEHAVIOR INSTRUCTIONS\n" +
		systemPrompt + "\n\n" +
		"# USER MESSAGE\n" +
		prompt
	log.Info("Prepending system prompt to user prompt with clear delimiters")
	return o.StartNewConversationWithOptions(finalPrompt, &clients.OpenCodeOptions{
		WorkDir: workDir,
	})
}

// ContinueConversationInDir continues an existing conversation in a specific directory
func (o *OpenCodeService) ContinueConversationInDir(sessionID, prompt, workDir string) (*services.CLIAgentResult, error) {
	return o.ContinueConversationWithOptions(sessionID, prompt, &clients.OpenCodeOptions{
		WorkDir: workDir,
	})
}

func (o *OpenCodeService) ContinueConversationWithOptions(
	sessionID, prompt string,
	options *clients.OpenCodeOptions,
) (*services.CLIAgentResult, error) {
	log.Info("📋 Starting to continue OpenCode conversation: %s", sessionID)

	finalOptions := o.deriveOpenCodeOptions(options)

	rawOutput, err := o.openCodeClient.ContinueSession(sessionID, prompt, finalOptions)
	if err != nil {
		log.Error("Failed to continue OpenCode session: %v", err)
		return nil, o.handleOpenCodeClientError(err, "failed to continue OpenCode session")
	}

	// Always log the OpenCode session
	logPath, writeErr := o.writeOpenCodeSessionLog(rawOutput)
	if writeErr != nil {
		log.Error("Failed to write OpenCode session log: %v", writeErr)
	}

	messages, err := MapOpenCodeOutputToMessages(rawOutput)
	if err != nil {
		log.Error("Failed to parse OpenCode output: %v", err)

		return nil, &core.ClaudeParseError{
			Message:     fmt.Sprintf("couldn't parse opencode response and instead stored the response in %s", logPath),
			LogFilePath: logPath,
			OriginalErr: err,
		}
	}

	actualSessionID := ExtractOpenCodeSessionID(messages)
	output, err := ExtractOpenCodeResult(messages)
	if err != nil {
		log.Error("Failed to extract OpenCode result: %v", err)
		return nil, fmt.Errorf("failed to extract OpenCode result: %w", err)
	}

	log.Info("📋 OpenCode response extracted successfully, session: %s, output length: %d", actualSessionID, len(output))
	result := &services.CLIAgentResult{
		Output:    output,
		SessionID: actualSessionID,
	}

	log.Info("📋 Completed successfully - continued OpenCode conversation with session: %s", actualSessionID)
	return result, nil
}

// handleOpenCodeClientError processes errors from OpenCode client calls.
func (o *OpenCodeService) handleOpenCodeClientError(err error, operation string) error {
	if err == nil {
		return nil
	}

	// Check if this is an OpenCode command error (reusing Claude error type)
	claudeErr, isClaudeErr := core.IsClaudeCommandErr(err)
	if !isClaudeErr {
		// Not a command error, return original error wrapped
		return fmt.Errorf("%s: %w", operation, err)
	}

	// Try to parse the output as OpenCode messages using internal parsing
	messages, parseErr := MapOpenCodeOutputToMessages(claudeErr.Output)
	if parseErr != nil {
		// If parsing fails, return original error wrapped
		log.Error("Failed to parse OpenCode output from error: %v", parseErr)
		return fmt.Errorf("%s: %w", operation, err)
	}

	// Try to extract the result even from errors
	output, extractErr := ExtractOpenCodeResult(messages)
	if extractErr == nil && output != "" {
		log.Info("✅ Successfully extracted OpenCode result from error: %s", output)
		return fmt.Errorf("%s: %s", operation, output)
	}

	// No result found, return original error wrapped
	log.Info("⚠️ No result found in OpenCode command output, returning original error")
	return fmt.Errorf("%s: %w", operation, err)
}

// AgentName identifies this service implementation
func (o *OpenCodeService) AgentName() string {
	return "opencode"
}

