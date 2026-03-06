package claude

import (
	"encoding/json"
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

type ClaudeService struct {
	claudeClient    clients.ClaudeClient
	logDir          string
	model           string
	agentsApiClient *clients.AgentsApiClient
	envManager      EnvManager
	progressEmitter services.ProgressEmitter
}

// EnvManager defines the interface for environment variable management
type EnvManager interface {
	Set(key, value string) error
}

func NewClaudeService(
	claudeClient clients.ClaudeClient,
	logDir string,
	model string,
	agentsApiClient *clients.AgentsApiClient,
	envManager EnvManager,
) *ClaudeService {
	return &ClaudeService{
		claudeClient:    claudeClient,
		logDir:          logDir,
		model:           model,
		agentsApiClient: agentsApiClient,
		envManager:      envManager,
	}
}

// writeClaudeSessionLog writes Claude output to a timestamped log file and returns the filepath
func (c *ClaudeService) writeClaudeSessionLog(rawOutput string) (string, error) {
	if err := os.MkdirAll(c.logDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create log directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("claude-session-%s.log", timestamp)
	filepath := filepath.Join(c.logDir, filename)

	if err := os.WriteFile(filepath, []byte(rawOutput), 0600); err != nil {
		return "", fmt.Errorf("failed to write log file: %w", err)
	}

	return filepath, nil
}

// CleanupOldLogs removes log files older than the specified number of days
func (c *ClaudeService) CleanupOldLogs(maxAgeDays int) error {
	log.Info("📋 Starting to cleanup old Claude session logs older than %d days", maxAgeDays)

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

		// Only clean up claude session log files
		if !strings.HasPrefix(file.Name(), "claude-session-") || !strings.HasSuffix(file.Name(), ".log") {
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

	log.Info("📋 Completed successfully - removed %d old Claude session log files", removedCount)
	return nil
}

// mergeOptions merges the service model with provided options
func (c *ClaudeService) mergeOptions(options *clients.ClaudeOptions) *clients.ClaudeOptions {
	merged := &clients.ClaudeOptions{}

	if options != nil {
		*merged = *options
	}

	// Only set model if not already specified in options and service has a model
	if merged.Model == "" && c.model != "" {
		merged.Model = c.model
	}

	return merged
}

func (c *ClaudeService) StartNewConversation(prompt string) (*services.CLIAgentResult, error) {
	return c.StartNewConversationWithOptions(prompt, nil)
}

func (c *ClaudeService) StartNewConversationWithOptions(
	prompt string,
	options *clients.ClaudeOptions,
) (*services.CLIAgentResult, error) {
	log.Info("📋 Starting to start new Claude conversation")

	// Merge service model with options
	mergedOptions := c.mergeOptions(options)

	// Create progress callback if emitter is set
	var onLine clients.ProgressCallback
	if c.progressEmitter != nil {
		emitter := c.progressEmitter
		onLine = func(line []byte) {
			if progress := services.MapClaudeLineToProgress(line); progress != nil {
				emitter(*progress)
			}
		}
	}

	rawOutput, err := c.claudeClient.StartNewSession(prompt, mergedOptions, onLine)
	if err != nil {
		log.Error("Failed to start new Claude session: %v", err)
		handledErr := c.handleClaudeClientError(err, "failed to start new Claude session")

		// Check if this is actually a successful response despite CLI exit code
		if successResp, ok := core.IsClaudeCLISuccessfulResponse(handledErr); ok {
			log.Info("📋 Recovered successful response from CLI error, session: %s", successResp.SessionID)
			return &services.CLIAgentResult{
				Output:    successResp.Result,
				SessionID: successResp.SessionID,
			}, nil
		}

		return nil, handledErr
	}

	// Always log the Claude session
	logPath, writeErr := c.writeClaudeSessionLog(rawOutput)
	if writeErr != nil {
		log.Error("Failed to write Claude session log: %v", writeErr)
	}

	log.Info("Raw Claude output length=%d bytes, logPath=%s", len(rawOutput), logPath)

	messages, err := services.MapClaudeOutputToMessages(rawOutput)
	if err != nil {
		log.Error("Failed to parse Claude output: %v", err)

		return nil, &core.ClaudeParseError{
			Message:     fmt.Sprintf("couldn't parse claude response and instead stored the response in %s", logPath),
			LogFilePath: logPath,
			OriginalErr: err,
		}
	}

	sessionID := c.extractSessionID(messages)
	output, err := c.extractClaudeResult(messages)
	if err != nil {
		log.Error("Failed to extract Claude result: %v", err)
		return nil, fmt.Errorf("failed to extract Claude result: %w", err)
	}

	log.Info("Parsed %d messages from StartNewConversation", len(messages))
	log.Info("📋 Claude response extracted successfully, session: %s, output length: %d", sessionID, len(output))
	result := &services.CLIAgentResult{
		Output:    output,
		SessionID: sessionID,
	}

	log.Info("📋 Completed successfully - started new Claude conversation with session: %s", sessionID)
	return result, nil
}

func (c *ClaudeService) StartNewConversationWithSystemPrompt(
	prompt, systemPrompt string,
) (*services.CLIAgentResult, error) {
	return c.StartNewConversationWithOptions(prompt, &clients.ClaudeOptions{
		SystemPrompt: systemPrompt,
	})
}

func (c *ClaudeService) StartNewConversationWithDisallowedTools(
	prompt string,
	disallowedTools []string,
) (*services.CLIAgentResult, error) {
	return c.StartNewConversationWithOptions(prompt, &clients.ClaudeOptions{
		DisallowedTools: disallowedTools,
	})
}

func (c *ClaudeService) ContinueConversation(sessionID, prompt string) (*services.CLIAgentResult, error) {
	return c.ContinueConversationWithOptions(sessionID, prompt, nil)
}

// StartNewConversationInDir starts a new conversation in a specific working directory
func (c *ClaudeService) StartNewConversationInDir(prompt, workDir string) (*services.CLIAgentResult, error) {
	return c.StartNewConversationWithOptions(prompt, &clients.ClaudeOptions{
		WorkDir: workDir,
	})
}

// StartNewConversationWithSystemPromptInDir starts a new conversation with system prompt in a specific directory
func (c *ClaudeService) StartNewConversationWithSystemPromptInDir(
	prompt, systemPrompt, workDir string,
) (*services.CLIAgentResult, error) {
	return c.StartNewConversationWithOptions(prompt, &clients.ClaudeOptions{
		SystemPrompt: systemPrompt,
		WorkDir:      workDir,
	})
}

// ContinueConversationInDir continues an existing conversation in a specific directory
func (c *ClaudeService) ContinueConversationInDir(sessionID, prompt, workDir string) (*services.CLIAgentResult, error) {
	return c.ContinueConversationWithOptions(sessionID, prompt, &clients.ClaudeOptions{
		WorkDir: workDir,
	})
}

func (c *ClaudeService) ContinueConversationWithOptions(
	sessionID, prompt string,
	options *clients.ClaudeOptions,
) (*services.CLIAgentResult, error) {
	log.Info("📋 Starting to continue Claude conversation: %s", sessionID)

	// Merge service model with options
	mergedOptions := c.mergeOptions(options)

	// Create progress callback if emitter is set
	var onLine clients.ProgressCallback
	if c.progressEmitter != nil {
		emitter := c.progressEmitter
		onLine = func(line []byte) {
			if progress := services.MapClaudeLineToProgress(line); progress != nil {
				emitter(*progress)
			}
		}
	}

	rawOutput, err := c.claudeClient.ContinueSession(sessionID, prompt, mergedOptions, onLine)
	if err != nil {
		log.Error("Failed to continue Claude session: %v", err)
		handledErr := c.handleClaudeClientError(err, "failed to continue Claude session")

		// Check if this is actually a successful response despite CLI exit code
		if successResp, ok := core.IsClaudeCLISuccessfulResponse(handledErr); ok {
			log.Info("📋 Recovered successful response from CLI error, session: %s", successResp.SessionID)
			return &services.CLIAgentResult{
				Output:    successResp.Result,
				SessionID: successResp.SessionID,
			}, nil
		}

		return nil, handledErr
	}

	// Always log the Claude session
	logPath, writeErr := c.writeClaudeSessionLog(rawOutput)
	if writeErr != nil {
		log.Error("Failed to write Claude session log: %v", writeErr)
	}

	log.Info("Raw Claude output length=%d bytes, logPath=%s", len(rawOutput), logPath)

	messages, err := services.MapClaudeOutputToMessages(rawOutput)
	if err != nil {
		log.Error("Failed to parse Claude output: %v", err)

		return nil, &core.ClaudeParseError{
			Message:     fmt.Sprintf("couldn't parse claude response and instead stored the response in %s", logPath),
			LogFilePath: logPath,
			OriginalErr: err,
		}
	}

	actualSessionID := c.extractSessionID(messages)
	output, err := c.extractClaudeResult(messages)
	if err != nil {
		log.Error("Failed to extract Claude result: %v", err)
		return nil, fmt.Errorf("failed to extract Claude result: %w", err)
	}

	log.Info("Parsed %d messages from ContinueConversation", len(messages))
	log.Info("📋 Claude response extracted successfully, session: %s, output length: %d", actualSessionID, len(output))
	result := &services.CLIAgentResult{
		Output:    output,
		SessionID: actualSessionID,
	}

	log.Info("📋 Completed successfully - continued Claude conversation with session: %s", actualSessionID)
	return result, nil
}

func (c *ClaudeService) extractSessionID(messages []services.ClaudeMessage) string {
	// Iterate through all messages to find a valid session ID.
	// The first message may be an UnknownClaudeMessage (e.g., from non-JSON output
	// like Terms of Service notices) with an empty session ID.
	for _, msg := range messages {
		sessionID := msg.GetSessionID()
		if sessionID != "" {
			return sessionID
		}
	}
	return "unknown"
}

// isRealUserMessage checks if a UserMessage is real human input (not a tool_result message).
// Real user messages have Content as a JSON string, while tool_result messages have Content as an array.
func isRealUserMessage(userMsg services.UserMessage) bool {
	// Try to unmarshal as string first (real user input)
	var simpleContent string
	if err := json.Unmarshal(userMsg.Message.Content, &simpleContent); err == nil {
		return true
	}

	// Check if contains tool_result type
	contentStr := string(userMsg.Message.Content)
	return !strings.Contains(contentStr, `"type":"tool_result"`)
}

func (c *ClaudeService) extractClaudeResult(messages []services.ClaudeMessage) (string, error) {
	// First priority: Look for ExitPlanMode messages (highest priority)
	for i := len(messages) - 1; i >= 0; i-- {
		if exitPlanMsg, ok := messages[i].(services.ExitPlanModeMessage); ok {
			plan := exitPlanMsg.GetPlan()
			if plan != "" {
				return plan, nil
			}
		}
	}

	// Track last result message to compare with assistant content later
	var resultText string
	// Second priority: Look for result message type
	for i := len(messages) - 1; i >= 0; i-- {
		if resultMsg, ok := messages[i].(services.ResultMessage); ok {
			if resultMsg.Result != "" {
				resultText = resultMsg.Result
				break
			}
		}
	}

	// Third priority: Collect last two assistant messages with text content
	// Strategy: Get the last two unique assistant messages, compare their sizes
	// If the first is significantly larger than the second, include both (detailed + summary)
	// Otherwise, include only the last message
	const (
		sizeDifferenceThreshold = 5.0 // First message must be 5x larger than second to include both
	)

	type assistantText struct {
		messageID string
		text      string
	}

	var assistantMessages []assistantText
	lastUserIndex := -1

	// Find the last REAL user message (skip tool_result messages)
	for i := len(messages) - 1; i >= 0; i-- {
		if userMsg, ok := messages[i].(services.UserMessage); ok {
			if isRealUserMessage(userMsg) {
				lastUserIndex = i
				break
			}
		}
	}

	// Collect all assistant messages with text content (skip tool_use-only messages)
	for i := lastUserIndex + 1; i < len(messages); i++ {
		if assistantMsg, ok := messages[i].(services.AssistantMessage); ok {
			for _, contentRaw := range assistantMsg.Message.Content {
				var contentItem struct {
					Type string `json:"type"`
					Text string `json:"text,omitempty"`
				}
				if err := json.Unmarshal(contentRaw, &contentItem); err == nil {
					if contentItem.Type == "text" && contentItem.Text != "" {
						assistantMessages = append(assistantMessages, assistantText{
							messageID: assistantMsg.Message.ID,
							text:      contentItem.Text,
						})
					}
				}
			}
		}
	}

	if len(assistantMessages) == 0 {
		if resultText != "" {
			return resultText, nil
		}
		return "(agent returned no response)", nil
	}

	// Get the last two unique assistant messages
	var lastTwo []string
	seenMessageIDs := make(map[string]bool)

	// Scan backwards to get last two unique message IDs
	for i := len(assistantMessages) - 1; i >= 0 && len(lastTwo) < 2; i-- {
		msgID := assistantMessages[i].messageID
		if !seenMessageIDs[msgID] {
			seenMessageIDs[msgID] = true
			lastTwo = append([]string{assistantMessages[i].text}, lastTwo...) // prepend to maintain order
		}
	}

	// Decision logic based on number of messages and size comparison
	if len(lastTwo) == 1 {
		// Only one assistant message - return it
		if resultText != "" {
			if len(resultText) >= len(lastTwo[0]) {
				return resultText, nil
			}
		}
		return lastTwo[0], nil
	}

	// Two messages: compare sizes
	firstLen := len(lastTwo[0])
	secondLen := len(lastTwo[1])

	if firstLen > secondLen*int(sizeDifferenceThreshold) {
		// First message is significantly larger (5x+) - likely detailed content followed by brief summary
		// Example: 10KB table breakdown + "Perfect! 60 columns" → return both
		assistantOutput := strings.Join(lastTwo, "\n\n")
		if resultText != "" {
			if len(resultText) >= len(assistantOutput) {
				return resultText, nil
			}
		}
		return assistantOutput, nil
	}

	// Messages are similar in size - return only the last one
	// Example: Two similar-sized summaries → return the final one
	assistantOutput := lastTwo[1]
	if resultText != "" {
		if len(resultText) >= len(assistantOutput) {
			return resultText, nil
		}
	}
	return assistantOutput, nil
}

// handleClaudeClientError processes errors from Claude client calls.
// If the error is a Claude command error, it attempts to extract the assistant message
// and returns a new error with the clean message. Otherwise, returns the original error.
func (c *ClaudeService) handleClaudeClientError(err error, operation string) error {
	if err == nil {
		return nil
	}

	// Check if this is a Claude command error
	claudeErr, isClaudeErr := core.IsClaudeCommandErr(err)
	if !isClaudeErr {
		// Not a Claude command error, return original error wrapped
		return fmt.Errorf("%s: %w", operation, err)
	}

	// Try to parse the output as Claude messages using internal parsing
	messages, parseErr := services.MapClaudeOutputToMessages(claudeErr.Output)
	if parseErr != nil {
		// If parsing fails, return original error wrapped
		log.Error("Failed to parse Claude output from error: %v", parseErr)
		return fmt.Errorf("%s: %w", operation, err)
	}

	// Check if this is actually a successful response despite CLI exit code.
	// Claude CLI can exit non-zero due to bugs in finalization/cleanup even when
	// the response was successful (is_error: false). See:
	// https://github.com/anthropics/claude-code-action/issues/846
	sessionID := c.extractSessionID(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		if resultMsg, ok := messages[i].(services.ResultMessage); ok {
			if !resultMsg.IsError && resultMsg.Result != "" {
				log.Info("⚠️ CLI exited non-zero but is_error=false, treating as success. Session: %s", sessionID)
				return &core.ErrClaudeCLISuccessfulResponse{
					Result:    resultMsg.Result,
					SessionID: sessionID,
				}
			}
			break // Only check the last result message
		}
	}

	// Check if CLI crashed mid-stream before emitting a result event.
	// The CLI may have produced valid assistant messages but crashed/died
	// before writing the final {"type":"result",...} JSON event.
	// In this case, we should recover the valid response instead of failing.
	hasResultMessage := false
	for _, msg := range messages {
		if _, ok := msg.(services.ResultMessage); ok {
			hasResultMessage = true
			break
		}
	}

	if !hasResultMessage {
		output, extractErr := c.extractClaudeResult(messages)
		if extractErr == nil && output != "" && output != "(agent returned no response)" {
			log.Info(
				"⚠️ CLI exited non-zero with no result event, "+
					"recovering assistant response. Session: %s",
				sessionID,
			)
			return &core.ErrClaudeCLISuccessfulResponse{
				Result:    output,
				SessionID: sessionID,
			}
		}
	}

	// First priority: Try to extract ExitPlanMode message (highest priority)
	for i := len(messages) - 1; i >= 0; i-- {
		if exitPlanMsg, ok := messages[i].(services.ExitPlanModeMessage); ok {
			plan := exitPlanMsg.GetPlan()
			if plan != "" {
				log.Info("✅ Successfully extracted Claude ExitPlanMode message from error: %s", plan)
				return fmt.Errorf("%s: %s", operation, plan)
			}
		}
	}

	// Second priority: Try to extract the result message (when is_error is true)
	for i := len(messages) - 1; i >= 0; i-- {
		if resultMsg, ok := messages[i].(services.ResultMessage); ok {
			if resultMsg.Result != "" {
				log.Info("✅ Successfully extracted Claude result message from error: %s", resultMsg.Result)
				return fmt.Errorf("%s: %s", operation, resultMsg.Result)
			}
		}
	}

	// Third priority: Fallback to assistant message (existing logic)
	for i := len(messages) - 1; i >= 0; i-- {
		if assistantMsg, ok := messages[i].(services.AssistantMessage); ok {
			for _, contentRaw := range assistantMsg.Message.Content {
				// Parse the content to check if it's a text content item
				var contentItem struct {
					Type string `json:"type"`
					Text string `json:"text,omitempty"`
				}
				if err := json.Unmarshal(contentRaw, &contentItem); err == nil {
					if contentItem.Type == "text" && contentItem.Text != "" {
						log.Info("✅ Successfully extracted Claude assistant message from error: %s", contentItem.Text)
						return fmt.Errorf("%s: %s", operation, contentItem.Text)
					}
				}
			}
		}
	}

	// No assistant message found, return original error wrapped
	log.Info("⚠️ No assistant message found in Claude command output, returning original error")
	return fmt.Errorf("%s: %w", operation, err)
}

// SetProgressEmitter sets the progress emitter for streaming progress updates
func (c *ClaudeService) SetProgressEmitter(emitter services.ProgressEmitter) {
	c.progressEmitter = emitter
}

// AgentName identifies this service implementation
func (c *ClaudeService) AgentName() string {
	return "claude"
}
