package models

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// RepositoryContext encapsulates information about the git repository being worked on
type RepositoryContext struct {
	// RepoPath is the absolute path to the git repository (empty if no-repo mode)
	RepoPath string
	// IsRepoMode indicates whether nairid is operating in repository mode
	IsRepoMode bool
	// RepositoryIdentifier is the owner/repo-name format identifier (e.g., "anthropics/nairid")
	RepositoryIdentifier string
}

// JobStatus represents the current state of a job
type JobStatus string

const (
	JobStatusInProgress JobStatus = "in_progress"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed" // Job failed due to unrecoverable error (e.g., Claude session crash)
)

// JobData tracks the state of a specific job/conversation
type JobData struct {
	JobID              string    `json:"job_id"`
	BranchName         string    `json:"branch_name"`
	WorktreePath       string    `json:"worktree_path,omitempty"` // Path to the job's git worktree (empty if using main repo)
	ClaudeSessionID    string    `json:"claude_session_id"`
	PullRequestID      string    `json:"pull_request_id"`            // GitHub PR number (e.g., "123") - empty if no PR created yet
	LastMessage        string    `json:"last_message"`               // The last message sent to Claude for this job
	ProcessedMessageID string    `json:"processed_message_id"`       // ID of the chat platform message being processed
	MessageLink        string    `json:"message_link"`               // Link to the original chat message
	Status             JobStatus `json:"status"`                     // Current status of the job: "in_progress" or "completed"
	Mode               AgentMode `json:"mode"`                       // "execute" or "ask" - determines if agent can modify files
	SystemPrompt       string    `json:"system_prompt,omitempty"`    // The first message's instructions, to start the job over if a stop leaves it without a session
	ReasoningEffort    string    `json:"reasoning_effort,omitempty"` // CLI effort level the backend sent when the conversation started; empty = model default
	EffortModel        string    `json:"effort_model,omitempty"`     // --model id the level was checked against; it applies only while the agent runs that model
	// Latest per-agent cap on one CLI run the backend sent; 0 means NAIRI_MAX_SESSION_MS or 1h.
	SessionTimeoutMs int64     `json:"session_timeout_ms,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// QueuedMessage represents a message that has been queued for processing but not yet started
type QueuedMessage struct {
	ProcessedMessageID string    `json:"processed_message_id"` // Unique identifier per chat message
	JobID              string    `json:"job_id"`               // Which conversation this belongs to
	MessageType        string    `json:"message_type"`         // "start_conversation_v1" or "user_message_v1"
	Message            string    `json:"message"`              // User's message text
	MessageLink        string    `json:"message_link"`         // Link to original chat message
	QueuedAt           time.Time `json:"queued_at"`            // When queued (for ordering)
	// The whole message payload, so recovery replays mode, attachments and history too.
	// Empty in entries written before it was added.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// OutboxMessage is a reply that has not reached the backend yet. It is kept until a send
// succeeds, so a restart or a backend outage delays it instead of losing it.
type OutboxMessage struct {
	ID       string          `json:"id"`
	Event    string          `json:"event"`
	Data     json.RawMessage `json:"data"`
	QueuedAt time.Time       `json:"queued_at"`
}

// PersistedState represents the state that gets persisted to disk
type PersistedState struct {
	AgentID        string                    `json:"agent_id"`
	Jobs           map[string]*JobData       `json:"jobs"`
	QueuedMessages map[string]*QueuedMessage `json:"queued_messages"` // Key: ProcessedMessageID
	// Written by versions before done_messages; read once to migrate, never written.
	SeenMessages map[string]time.Time `json:"seen_messages,omitempty"`
	// ProcessedMessageID → when its turn finished. Redeliveries of these are acked, not rerun.
	DoneMessages map[string]time.Time      `json:"done_messages,omitempty"`
	Outbox       map[string]*OutboxMessage `json:"outbox,omitempty"`
}

// LoadedState represents the result of loading persisted state from disk
type LoadedState struct {
	AgentID        string
	Jobs           map[string]*JobData
	QueuedMessages map[string]*QueuedMessage
	SeenMessages   map[string]time.Time
	DoneMessages   map[string]time.Time
	Outbox         map[string]*OutboxMessage
	Loaded         bool // Indicates whether state was successfully loaded from disk
}

// AppState manages the state of all active jobs
type AppState struct {
	agentID        string
	jobs           map[string]*JobData
	queuedMessages map[string]*QueuedMessage
	doneMessages   map[string]time.Time
	outbox         map[string]*OutboxMessage
	statePath      string
	repoContext    *RepositoryContext
	mutex          sync.RWMutex
}

// NewAppState creates a new AppState instance
func NewAppState(agentID string, statePath string) *AppState {
	return &AppState{
		agentID:        agentID,
		jobs:           make(map[string]*JobData),
		queuedMessages: make(map[string]*QueuedMessage),
		doneMessages:   make(map[string]time.Time),
		outbox:         make(map[string]*OutboxMessage),
		statePath:      statePath,
		repoContext:    &RepositoryContext{}, // Initialize with empty context
	}
}

// SetRepositoryContext sets the repository context for this app state
func (a *AppState) SetRepositoryContext(ctx *RepositoryContext) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.repoContext = ctx
}

// GetRepositoryContext returns a copy of the repository context
func (a *AppState) GetRepositoryContext() *RepositoryContext {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	if a.repoContext == nil {
		return &RepositoryContext{}
	}
	return &RepositoryContext{
		RepoPath:             a.repoContext.RepoPath,
		IsRepoMode:           a.repoContext.IsRepoMode,
		RepositoryIdentifier: a.repoContext.RepositoryIdentifier,
	}
}

// GetAgentID returns the agent ID
func (a *AppState) GetAgentID() string {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	return a.agentID
}

// UpdateJobData updates or creates job data for a given JobID
func (a *AppState) UpdateJobData(jobID string, data JobData) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.jobs[jobID] = &data

	// Persist state after updating
	if err := a.persistStateLocked(); err != nil {
		return fmt.Errorf("failed to persist state: %w", err)
	}

	return nil
}

// SetJobPullRequestID updates only the PR ID of an existing job under the lock.
// Callers holding an older JobData snapshot must use this instead of UpdateJobData,
// otherwise they overwrite fields (e.g. Status) written since the snapshot was taken.
// Returns false if the job no longer exists.
func (a *AppState) SetJobPullRequestID(jobID string, prID string) (bool, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	data, exists := a.jobs[jobID]
	if !exists {
		return false, nil
	}
	data.PullRequestID = prID

	if err := a.persistStateLocked(); err != nil {
		return true, fmt.Errorf("failed to persist state: %w", err)
	}

	return true, nil
}

// GetJobData retrieves job data for a given JobID
// FailJobIfInProgress marks the job failed only while it is still in progress for this message,
// so an old turn unwinding after a newer one took the job cannot fail the newer one.
func (a *AppState) FailJobIfInProgress(jobID, processedMessageID string) (bool, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	data, exists := a.jobs[jobID]
	if !exists || data.Status != JobStatusInProgress || data.ProcessedMessageID != processedMessageID {
		return false, nil
	}
	data.Status = JobStatusFailed
	data.UpdatedAt = time.Now()

	if err := a.persistStateLocked(); err != nil {
		return true, fmt.Errorf("failed to persist state: %w", err)
	}

	return true, nil
}

func (a *AppState) GetJobData(jobID string) (*JobData, bool) {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	data, exists := a.jobs[jobID]
	if !exists {
		return nil, false
	}
	// Return a copy to avoid race conditions. JobData holds only values, so
	// copying the struct copies every field, including ones added later.
	jobCopy := *data
	return &jobCopy, true
}

// RemoveJob removes job data for a given JobID
func (a *AppState) RemoveJob(jobID string) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	delete(a.jobs, jobID)

	// Persist state after removing
	if err := a.persistStateLocked(); err != nil {
		return fmt.Errorf("failed to persist state: %w", err)
	}

	return nil
}

// GetAllJobs returns a copy of all job data
func (a *AppState) GetAllJobs() map[string]JobData {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	result := make(map[string]JobData)
	for jobID, data := range a.jobs {
		result[jobID] = *data
	}
	return result
}

// AddQueuedMessage adds a queued message to the state and persists it
func (a *AppState) AddQueuedMessage(msg QueuedMessage) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.queuedMessages[msg.ProcessedMessageID] = &msg

	// Persist state after adding
	if err := a.persistStateLocked(); err != nil {
		return fmt.Errorf("failed to persist state: %w", err)
	}

	return nil
}

// RemoveQueuedMessage removes a queued message from the state and persists
func (a *AppState) RemoveQueuedMessage(processedMessageID string) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	delete(a.queuedMessages, processedMessageID)

	// Persist state after removing
	if err := a.persistStateLocked(); err != nil {
		return fmt.Errorf("failed to persist state: %w", err)
	}

	return nil
}

// RemoveQueuedMessagesForJob removes all queued messages for a given job ID and persists
func (a *AppState) RemoveQueuedMessagesForJob(jobID string) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	removed := 0
	for msgID, msg := range a.queuedMessages {
		if msg.JobID == jobID {
			delete(a.queuedMessages, msgID)
			removed++
		}
	}

	if removed == 0 {
		return nil
	}

	if err := a.persistStateLocked(); err != nil {
		return fmt.Errorf("failed to persist state: %w", err)
	}

	return nil
}

// GetAllQueuedMessages returns a copy of all queued messages
func (a *AppState) GetAllQueuedMessages() []QueuedMessage {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	result := make([]QueuedMessage, 0, len(a.queuedMessages))
	for _, msg := range a.queuedMessages {
		result = append(result, QueuedMessage{
			ProcessedMessageID: msg.ProcessedMessageID,
			JobID:              msg.JobID,
			MessageType:        msg.MessageType,
			Message:            msg.Message,
			MessageLink:        msg.MessageLink,
			QueuedAt:           msg.QueuedAt,
			Payload:            msg.Payload,
		})
	}
	return result
}

// FinishMessage records that the message's turn is over and takes it off the recovery
// queue, in one write so a crash cannot leave it queued but done, or gone but not done.
func (a *AppState) FinishMessage(processedMessageID string, at time.Time) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.doneMessages[processedMessageID] = at
	delete(a.queuedMessages, processedMessageID)

	if err := a.persistStateLocked(); err != nil {
		return fmt.Errorf("failed to persist state: %w", err)
	}

	return nil
}

// Restore loads a whole persisted snapshot and writes it back once.
func (a *AppState) Restore(jobs map[string]*JobData, queued map[string]*QueuedMessage, done map[string]time.Time, outbox map[string]*OutboxMessage) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	for jobID, jobData := range jobs {
		copied := *jobData
		a.jobs[jobID] = &copied
	}
	for msgID, queuedMsg := range queued {
		copied := *queuedMsg
		a.queuedMessages[msgID] = &copied
	}
	for msgID, doneAt := range done {
		a.doneMessages[msgID] = doneAt
	}
	for id, outboxMsg := range outbox {
		copied := *outboxMsg
		a.outbox[id] = &copied
	}

	if err := a.persistStateLocked(); err != nil {
		return fmt.Errorf("failed to persist state: %w", err)
	}

	return nil
}

func (a *AppState) IsMessageDone(processedMessageID string) bool {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	_, done := a.doneMessages[processedMessageID]
	return done
}

// CleanupDoneMessages forgets messages finished before cutoff and persists state
func (a *AppState) CleanupDoneMessages(cutoff time.Time) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	removed := 0
	for msgID, doneAt := range a.doneMessages {
		if doneAt.Before(cutoff) {
			delete(a.doneMessages, msgID)
			removed++
		}
	}
	if removed == 0 {
		return nil
	}

	if err := a.persistStateLocked(); err != nil {
		return fmt.Errorf("failed to persist state: %w", err)
	}

	return nil
}

func (a *AppState) AddOutboxMessage(msg OutboxMessage) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.outbox[msg.ID] = &msg

	if err := a.persistStateLocked(); err != nil {
		return fmt.Errorf("failed to persist state: %w", err)
	}

	return nil
}

func (a *AppState) RemoveOutboxMessage(id string) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if _, exists := a.outbox[id]; !exists {
		return nil
	}
	delete(a.outbox, id)

	if err := a.persistStateLocked(); err != nil {
		return fmt.Errorf("failed to persist state: %w", err)
	}

	return nil
}

// GetAllOutboxMessages returns the unsent replies, oldest first
func (a *AppState) GetAllOutboxMessages() []OutboxMessage {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	result := make([]OutboxMessage, 0, len(a.outbox))
	for _, msg := range a.outbox {
		result = append(result, *msg)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].QueuedAt.Before(result[j].QueuedAt) })
	return result
}

// persistStateLocked persists the current state to disk
// MUST be called with mutex already locked
func (a *AppState) persistStateLocked() error {
	if a.statePath == "" {
		return fmt.Errorf("state path not configured")
	}

	// Create the state object
	state := PersistedState{
		AgentID:        a.agentID,
		Jobs:           a.jobs,
		QueuedMessages: a.queuedMessages,
		DoneMessages:   a.doneMessages,
		Outbox:         a.outbox,
	}

	// Marshal to JSON with pretty printing
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(a.statePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// Write to temporary file first
	tempPath := a.statePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp state file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempPath, a.statePath); err != nil {
		return fmt.Errorf("failed to rename state file: %w", err)
	}
	if afterPersist != nil {
		afterPersist(a.statePath)
	}

	return nil
}

// afterPersist lets tests look at every state file written, each a point a crash could leave behind.
var afterPersist func(statePath string)

// LoadState loads persisted state from disk
// Returns LoadedState containing the loaded data and a boolean indicating success, or an error
func LoadState(statePath string) (*LoadedState, error) {
	// Check if state file exists
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		return &LoadedState{
			AgentID:        "",
			Jobs:           nil,
			QueuedMessages: nil,
			Loaded:         false,
		}, nil
	}

	// Read the state file
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	// Unmarshal the state
	var state PersistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return &LoadedState{
		AgentID:        state.AgentID,
		Jobs:           state.Jobs,
		QueuedMessages: state.QueuedMessages,
		SeenMessages:   state.SeenMessages,
		DoneMessages:   state.DoneMessages,
		Outbox:         state.Outbox,
		Loaded:         true,
	}, nil
}
