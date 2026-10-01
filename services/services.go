package services

import "nairid/models"

// CLIAgentResult represents the result of a CLI agent conversation.
// Cost and Usage fields are optional; harnesses that do not report them
// (e.g. Cursor) leave them nil and the backend persists NULL columns.
type CLIAgentResult struct {
	Output    string
	SessionID string
	Usage     *CLIAgentUsage
}

// CLIAgentUsage holds optional per-message cost and token usage data
// extracted from a CLI harness after a conversation turn completes.
// Every field is a pointer because no single harness populates all of
// them: Cursor reports nothing, Codex cannot report cache writes,
// OpenCode is best-effort, and Claude may omit cost on cache-only
// turns. Use nil to mean "the harness did not report this value" so
// callers can persist NULL rather than a misleading zero/empty value.
type CLIAgentUsage struct {
	CostUSD          *float64
	InputTokens      *int64
	OutputTokens     *int64
	CacheReadTokens  *int64
	CacheWriteTokens *int64
	Model            *string
}

// ProgressEmitter is a callback for emitting progress messages during agent execution
type ProgressEmitter func(progress models.AgentProgressPayload)

// CLIAgent defines the interface for CLI agent operations like Claude Code, Cursor, etc.
type CLIAgent interface {
	// StartNewConversation starts a new conversation with a prompt
	StartNewConversation(prompt string) (*CLIAgentResult, error)

	// StartNewConversationWithSystemPrompt starts a new conversation with both user and system prompts
	StartNewConversationWithSystemPrompt(prompt, systemPrompt string) (*CLIAgentResult, error)

	// StartNewConversationWithDisallowedTools starts a new conversation with specified disallowed tools
	StartNewConversationWithDisallowedTools(prompt string, disallowedTools []string) (*CLIAgentResult, error)

	// ContinueConversation continues an existing conversation
	ContinueConversation(sessionID, prompt string) (*CLIAgentResult, error)

	// ContinueConversationWithSystemPrompt continues an existing conversation with a system prompt
	ContinueConversationWithSystemPrompt(sessionID, prompt, systemPrompt string) (*CLIAgentResult, error)

	// StartNewConversationInDir starts a new conversation in a specific working directory
	StartNewConversationInDir(prompt, workDir string) (*CLIAgentResult, error)

	// StartNewConversationWithSystemPromptInDir starts a new conversation with system prompt in a specific directory
	StartNewConversationWithSystemPromptInDir(prompt, systemPrompt, workDir string) (*CLIAgentResult, error)

	// ContinueConversationInDir continues an existing conversation in a specific directory
	ContinueConversationInDir(sessionID, prompt, workDir string) (*CLIAgentResult, error)

	// ContinueConversationWithSystemPromptInDir continues an existing conversation with a system prompt in a specific directory
	ContinueConversationWithSystemPromptInDir(sessionID, prompt, systemPrompt, workDir string) (*CLIAgentResult, error)

	// StartNewConversationWithProgress starts a new conversation with progress streaming.
	// Empty systemPrompt, workDir or effort are ignored. Effort is a CLI reasoning
	// level (low/medium/high/xhigh/max) and must be passed on every call: no CLI keeps
	// it across a resume.
	StartNewConversationWithProgress(prompt, systemPrompt, workDir, effort string, emitter ProgressEmitter) (*CLIAgentResult, error)

	// ContinueConversationWithProgress continues a conversation with progress streaming.
	// Empty systemPrompt, workDir or effort are ignored.
	ContinueConversationWithProgress(sessionID, prompt, systemPrompt, workDir, effort string, emitter ProgressEmitter) (*CLIAgentResult, error)

	// Model returns the model this agent was started with, after agent defaults
	// are applied. Empty when the CLI picks its own default.
	Model() string

	// CleanupOldLogs removes old log files based on age
	CleanupOldLogs(maxAgeDays int) error

	// AgentName returns the identifier for the concrete agent implementation
	// (e.g., "claude" or "cursor") so callers can adapt behavior per agent
	AgentName() string
}
