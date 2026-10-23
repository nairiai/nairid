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
}

func newTurnRegistry() *turnRegistry {
	return &turnRegistry{turns: map[string]*runningTurn{}, seenStopRequests: map[string]string{}}
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
