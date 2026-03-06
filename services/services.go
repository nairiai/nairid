package services

// CLIAgentResult represents the result of a CLI agent conversation
type CLIAgentResult struct {
	Output    string
	SessionID string
}

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

	// CleanupOldLogs removes old log files based on age
	CleanupOldLogs(maxAgeDays int) error

	// AgentName returns the identifier for the concrete agent implementation
	// (e.g., "claude" or "cursor") so callers can adapt behavior per agent
	AgentName() string
}
