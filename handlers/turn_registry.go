package handlers

import (
	"context"
	"sync"
)

type runningTurn struct {
	ctx           context.Context
	cancel        context.CancelFunc
	stopRequested bool
	stoppedBy     string
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

func (r *turnRegistry) Stop(jobID, requestedBy string) (running bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	turn, exists := r.turns[jobID]
	if !exists {
		return false
	}
	turn.stopRequested = true
	turn.stoppedBy = requestedBy
	turn.cancel()
	return true
}

func (r *turnRegistry) StopRequested(jobID string) (requested bool, stoppedBy string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	turn, exists := r.turns[jobID]
	if !exists {
		return false, ""
	}
	return turn.stopRequested, turn.stoppedBy
}

func (r *turnRegistry) End(jobID string, turn *runningTurn) (stopRequested bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	turn.cancel()
	if r.turns[jobID] == turn {
		delete(r.turns, jobID)
	}
	return turn.stopRequested
}

// The same request can arrive over the socket and again from the job poll.
func (r *turnRegistry) isRepeatedStopRequest(jobID, requestedAt string) bool {
	if requestedAt == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seenStopRequests[jobID] == requestedAt {
		return true
	}
	r.seenStopRequests[jobID] = requestedAt
	return false
}
