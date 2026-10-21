package handlers

import (
	"context"
	"sync"
)

type runningTurn struct {
	cancel             context.CancelFunc
	processedMessageID string
	stopRequested      bool
	stoppedBy          string
}

// turnRegistry knows which job is running a CLI turn right now and how to end it.
// The stop flag, not the CLI's exit code, says whether a turn was stopped: Claude
// exits 0 after SIGINT and Codex exits 1.
type turnRegistry struct {
	mu               sync.Mutex
	turns            map[string]*runningTurn
	seenStopRequests map[string]string // job id → requested_at already answered
}

func newTurnRegistry() *turnRegistry {
	return &turnRegistry{turns: map[string]*runningTurn{}, seenStopRequests: map[string]string{}}
}

// Begin registers the turn and returns the context its CLI run must use.
func (r *turnRegistry) Begin(jobID, processedMessageID string) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	defer r.mu.Unlock()
	turn := &runningTurn{cancel: cancel, processedMessageID: processedMessageID}
	// A reply with no session is re-run as a conversation start inside the same
	// handler call; a stop asked for in between must not get lost in the hand-over.
	if previous, exists := r.turns[jobID]; exists {
		previous.cancel()
		turn.stopRequested = previous.stopRequested
		turn.stoppedBy = previous.stoppedBy
	}
	if turn.stopRequested {
		cancel()
	}
	r.turns[jobID] = turn
	return ctx
}

// Stop ends the job's running turn. It reports whether a turn was running.
func (r *turnRegistry) Stop(jobID, requestedBy string) (processedMessageID string, running bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	turn, exists := r.turns[jobID]
	if !exists {
		return "", false
	}
	turn.stopRequested = true
	turn.stoppedBy = requestedBy
	turn.cancel()
	return turn.processedMessageID, true
}

// End forgets the turn and reports whether a stop was asked for while it ran.
// Calling it again for the same turn is a no-op.
func (r *turnRegistry) End(jobID string) (stopped bool, stoppedBy string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	turn, exists := r.turns[jobID]
	if !exists {
		return false, ""
	}
	delete(r.turns, jobID)
	turn.cancel()
	return turn.stopRequested, turn.stoppedBy
}

// FirstSightOf reports whether this stop request (job + requested_at) is new. The
// same request can arrive over the socket and again from the job poll.
func (r *turnRegistry) FirstSightOf(jobID, requestedAt string) bool {
	if requestedAt == "" {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seenStopRequests[jobID] == requestedAt {
		return false
	}
	r.seenStopRequests[jobID] = requestedAt
	return true
}
