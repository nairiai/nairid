package clients

// ProgressCallback is called for each NDJSON line read from CLI stdout during execution.
// The line is trimmed of whitespace but not parsed — parsing is done at the service layer.
type ProgressCallback func(line []byte)

// ClaudeOptions contains optional parameters for Claude CLI interactions
type ClaudeOptions struct {
	SystemPrompt    string
	DisallowedTools []string
	Model           string // Model alias or full name (e.g., "sonnet", "haiku", "opus", "claude-sonnet-4-5-20250929")
	WorkDir         string // Working directory for the Claude session (e.g., a git worktree path)
}

// CursorOptions contains optional parameters for Cursor CLI interactions
type CursorOptions struct {
	SystemPrompt string
	Model        string
}

// CodexOptions contains optional parameters for Codex CLI interactions
type CodexOptions struct {
	Model     string // GPT-5 or other model
	Sandbox   string // "workspace-write", "danger-full-access", "read-only"
	WebSearch bool   // Enable --search flag
}

// ClaudeClient defines the interface for Claude CLI interactions
type ClaudeClient interface {
	StartNewSession(prompt string, options *ClaudeOptions, onLine ProgressCallback) (string, error)
	ContinueSession(sessionID, prompt string, options *ClaudeOptions, onLine ProgressCallback) (string, error)
}

// CursorClient defines the interface for Cursor CLI interactions
type CursorClient interface {
	StartNewSession(prompt string, options *CursorOptions) (string, error)
	ContinueSession(sessionID, prompt string, options *CursorOptions) (string, error)
}

// CodexClient defines the interface for Codex CLI interactions
type CodexClient interface {
	StartNewSession(prompt string, options *CodexOptions, onLine ProgressCallback) (string, error)
	ContinueSession(threadID, prompt string, options *CodexOptions, onLine ProgressCallback) (string, error)
}

// OpenCodeOptions contains optional parameters for OpenCode CLI interactions
type OpenCodeOptions struct {
	Model   string // Model in provider/model format (e.g., "anthropic/claude-3-5-sonnet")
	WorkDir string // Working directory for the OpenCode session (e.g., a git worktree path)
}

// OpenCodeClient defines the interface for OpenCode CLI interactions
type OpenCodeClient interface {
	StartNewSession(prompt string, options *OpenCodeOptions, onLine ProgressCallback) (string, error)
	ContinueSession(sessionID, prompt string, options *OpenCodeOptions, onLine ProgressCallback) (string, error)
}
