package cursor

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

type CursorService struct {
	cursorClient clients.CursorClient
	logDir       string
	model        string
}

func NewCursorService(cursorClient clients.CursorClient, logDir, model string) *CursorService {
	return &CursorService{
		cursorClient: cursorClient,
		logDir:       logDir,
		model:        model,
	}
}

// writeCursorSessionLog writes Cursor output to a timestamped log file and returns the filepath
func (c *CursorService) writeCursorSessionLog(rawOutput string) (string, error) {
	if err := os.MkdirAll(c.logDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create log directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("cursor-session-%s.log", timestamp)
	filepath := filepath.Join(c.logDir, filename)

	if err := os.WriteFile(filepath, []byte(rawOutput), 0600); err != nil {
		return "", fmt.Errorf("failed to write log file: %w", err)
	}

	return filepath, nil
}

// CleanupOldLogs removes log files older than the specified number of days
func (c *CursorService) CleanupOldLogs(maxAgeDays int) error {
	log.Info("📋 Starting to cleanup old Cursor session logs older than %d days", maxAgeDays)

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

		// Only clean up cursor session log files
		if !strings.HasPrefix(file.Name(), "cursor-session-") || !strings.HasSuffix(file.Name(), ".log") {
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

	log.Info("📋 Completed successfully - removed %d old Cursor session log files", removedCount)
	return nil
}

func (c *CursorService) StartNewConversation(prompt string) (*services.CLIAgentResult, error) {
	return c.StartNewConversationWithOptions(prompt, nil)
}

// deriveCursorOptions creates a final options struct, applying service model if set
func (c *CursorService) deriveCursorOptions(options *clients.CursorOptions) *clients.CursorOptions {
	finalOptions := options
	if c.model != "" {
		if finalOptions == nil {
			finalOptions = &clients.CursorOptions{Model: c.model}
		} else {
			// Create a copy to avoid modifying the original
			finalOptions = &clients.CursorOptions{
				SystemPrompt: finalOptions.SystemPrompt,
				Model:        c.model, // Service model takes precedence
			}
		}
	}
	return finalOptions
}

func (c *CursorService) StartNewConversationWithOptions(
	prompt string,
	options *clients.CursorOptions,
) (*services.CLIAgentResult, error) {
	log.Info("📋 Starting to start new Cursor conversation")

	finalOptions := c.deriveCursorOptions(options)

	rawOutput, err := c.cursorClient.StartNewSession(prompt, finalOptions)
	if err != nil {
		log.Error("Failed to start new Cursor session: %v", err)
		return nil, c.handleCursorClientError(err, "failed to start new Cursor session")
	}

	// Always log the Cursor session
	logPath, writeErr := c.writeCursorSessionLog(rawOutput)
	if writeErr != nil {
		log.Error("Failed to write Cursor session log: %v", writeErr)
	}

	messages, err := MapCursorOutputToMessages(rawOutput)
	if err != nil {
		log.Error("Failed to parse Cursor output: %v", err)

		return nil, &core.ClaudeParseError{ // Reusing Claude parse error for consistency
			Message:     fmt.Sprintf("couldn't parse cursor response and instead stored the response in %s", logPath),
			LogFilePath: logPath,
			OriginalErr: err,
		}
	}

	sessionID := ExtractCursorSessionID(messages)
	output, err := ExtractCursorResult(messages)
	if err != nil {
		log.Error("Failed to extract Cursor result: %v", err)
		return nil, fmt.Errorf("failed to extract Cursor result: %w", err)
	}

	log.Info("📋 Cursor response extracted successfully, session: %s, output length: %d", sessionID, len(output))
	result := &services.CLIAgentResult{
		Output:    output,
		SessionID: sessionID,
	}

	log.Info("📋 Completed successfully - started new Cursor conversation with session: %s", sessionID)
	return result, nil
}

func (c *CursorService) StartNewConversationWithSystemPrompt(
	prompt, systemPrompt string,
) (*services.CLIAgentResult, error) {
	return c.StartNewConversationWithOptions(prompt, &clients.CursorOptions{
		SystemPrompt: systemPrompt,
	})
}

func (c *CursorService) StartNewConversationWithDisallowedTools(
	prompt string,
	disallowedTools []string,
) (*services.CLIAgentResult, error) {
	return c.StartNewConversationWithOptions(prompt, &clients.CursorOptions{
		// DisallowedTools not supported in CursorOptions yet
	})
}

func (c *CursorService) ContinueConversation(sessionID, prompt string) (*services.CLIAgentResult, error) {
	return c.ContinueConversationWithOptions(sessionID, prompt, nil)
}

func (c *CursorService) ContinueConversationWithSystemPrompt(sessionID, prompt, systemPrompt string) (*services.CLIAgentResult, error) {
	// System prompt persists from turn 1 (prepended into user message), no action needed on continue
	return c.ContinueConversation(sessionID, prompt)
}

// StartNewConversationInDir starts a new conversation in a specific working directory
// Note: Cursor does not support custom working directories yet, falls back to default behavior
func (c *CursorService) StartNewConversationInDir(prompt, workDir string) (*services.CLIAgentResult, error) {
	log.Warn("⚠️ Cursor does not support custom working directories, ignoring workDir: %s", workDir)
	return c.StartNewConversation(prompt)
}

// StartNewConversationWithSystemPromptInDir starts a new conversation with system prompt in a specific directory
// Note: Cursor does not support custom working directories yet, falls back to default behavior
func (c *CursorService) StartNewConversationWithSystemPromptInDir(
	prompt, systemPrompt, workDir string,
) (*services.CLIAgentResult, error) {
	log.Warn("⚠️ Cursor does not support custom working directories, ignoring workDir: %s", workDir)
	return c.StartNewConversationWithSystemPrompt(prompt, systemPrompt)
}

// ContinueConversationInDir continues an existing conversation in a specific directory
// Note: Cursor does not support custom working directories yet, falls back to default behavior
func (c *CursorService) ContinueConversationInDir(sessionID, prompt, workDir string) (*services.CLIAgentResult, error) {
	log.Warn("Cursor does not support custom working directories, ignoring workDir: %s", workDir)
	return c.ContinueConversation(sessionID, prompt)
}

func (c *CursorService) ContinueConversationWithSystemPromptInDir(sessionID, prompt, systemPrompt, workDir string) (*services.CLIAgentResult, error) {
	// System prompt persists from turn 1 (prepended into user message), no action needed on continue
	return c.ContinueConversationInDir(sessionID, prompt, workDir)
}

func (c *CursorService) ContinueConversationWithOptions(
	sessionID, prompt string,
	options *clients.CursorOptions,
) (*services.CLIAgentResult, error) {
	log.Info("📋 Starting to continue Cursor conversation: %s", sessionID)

	finalOptions := c.deriveCursorOptions(options)

	rawOutput, err := c.cursorClient.ContinueSession(sessionID, prompt, finalOptions)
	if err != nil {
		log.Error("Failed to continue Cursor session: %v", err)
		return nil, c.handleCursorClientError(err, "failed to continue Cursor session")
	}

	// Always log the Cursor session
	logPath, writeErr := c.writeCursorSessionLog(rawOutput)
	if writeErr != nil {
		log.Error("Failed to write Cursor session log: %v", writeErr)
	}

	messages, err := MapCursorOutputToMessages(rawOutput)
	if err != nil {
		log.Error("Failed to parse Cursor output: %v", err)

		return nil, &core.ClaudeParseError{
			Message:     fmt.Sprintf("couldn't parse cursor response and instead stored the response in %s", logPath),
			LogFilePath: logPath,
			OriginalErr: err,
		}
	}

	actualSessionID := ExtractCursorSessionID(messages)
	output, err := ExtractCursorResult(messages)
	if err != nil {
		log.Error("Failed to extract Cursor result: %v", err)
		return nil, fmt.Errorf("failed to extract Cursor result: %w", err)
	}

	log.Info("📋 Cursor response extracted successfully, session: %s, output length: %d", actualSessionID, len(output))
	result := &services.CLIAgentResult{
		Output:    output,
		SessionID: actualSessionID,
	}

	log.Info("📋 Completed successfully - continued Cursor conversation with session: %s", actualSessionID)
	return result, nil
}

// handleCursorClientError processes errors from Cursor client calls.
func (c *CursorService) handleCursorClientError(err error, operation string) error {
	if err == nil {
		return nil
	}

	// Check if this is a Cursor command error (reusing Claude error type)
	claudeErr, isClaudeErr := core.IsClaudeCommandErr(err)
	if !isClaudeErr {
		// Not a command error, return original error wrapped
		return fmt.Errorf("%s: %w", operation, err)
	}

	// Try to parse the output as Cursor messages using internal parsing
	messages, parseErr := MapCursorOutputToMessages(claudeErr.Output)
	if parseErr != nil {
		// If parsing fails, return original error wrapped
		log.Error("Failed to parse Cursor output from error: %v", parseErr)
		return fmt.Errorf("%s: %w", operation, err)
	}

	// Try to extract the result message
	for i := len(messages) - 1; i >= 0; i-- {
		if resultMsg, ok := messages[i].(CursorResultMessage); ok {
			if resultMsg.Result != "" {
				log.Info("✅ Successfully extracted Cursor result message from error: %s", resultMsg.Result)
				return fmt.Errorf("%s: %s", operation, resultMsg.Result)
			}
		}
	}

	// No result message found, return original error wrapped
	log.Info("⚠️ No result message found in Cursor command output, returning original error")
	return fmt.Errorf("%s: %w", operation, err)
}

// AgentName identifies this service implementation
func (c *CursorService) AgentName() string {
	return "cursor"
}

