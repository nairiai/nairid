package handlers

import (
	"context"
	"sync"
)

type runningTurn struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type turnRegistry struct {
	mu               sync.Mutex
	turns            map[string]*runningTurn
	seenStopRequests map[string]string // job id → requested_at already handled
	released         map[string]bool   // jobs to forget once their running turn ends
}

func newTurnRegistry() *turnRegistry {
	return &turnRegistry{
		turns:            map[string]*runningTurn{},
		seenStopRequests: map[string]string{},
		released:         map[string]bool{},
	}
}

// releaseAfterTurn marks a running job to be forgotten when its turn ends; false when no turn runs.
func (r *turnRegistry) releaseAfterTurn(jobID string) (running bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, running = r.turns[jobID]; running {
		r.released[jobID] = true
	}
	return running
}

func (r *turnRegistry) isReleased(jobID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.released[jobID]
}

func (r *turnRegistry) takeReleased(jobID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	released := r.released[jobID]
	delete(r.released, jobID)
	return released
}

func (r *turnRegistry) Begin(jobID string) *runningTurn {
	ctx, cancel := context.WithCancel(context.Background())
	turn := &runningTurn{ctx: ctx, cancel: cancel}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turns[jobID] = turn
	return turn
}

func (r *turnRegistry) Stop(jobID string) (running bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	turn, exists := r.turns[jobID]
	if exists {
		turn.cancel()
	}
	return exists
}

func (r *turnRegistry) IsRunning(jobID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, running := r.turns[jobID]
	return running
}

func (r *turnRegistry) End(jobID string, turn *runningTurn) (stopped bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stopped = turn.ctx.Err() != nil
	turn.cancel()
	if r.turns[jobID] == turn {
		delete(r.turns, jobID)
	}
	return stopped
}

// The same request can arrive over the socket and again from the job poll.
func (r *turnRegistry) claimStopRequest(jobID, requestedAt string) (first bool) {
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
