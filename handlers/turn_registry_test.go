package handlers

import "testing"

func TestTurnRegistry_StopEndsTheRunningTurn(t *testing.T) {
	registry := newTurnRegistry()
	turn := registry.Begin("j1")

	if registry.Stop("j2") {
		t.Fatal("a job with no turn must not report a running turn")
	}
	if !registry.Stop("j1") {
		t.Fatal("Stop must find the running turn")
	}
	if turn.ctx.Err() == nil {
		t.Fatal("the turn's context must be cancelled by Stop")
	}
	if !registry.End("j1", turn) {
		t.Fatal("End must report the stop")
	}
	if registry.Stop("j1") {
		t.Fatal("an ended turn is not running")
	}
}

func TestTurnRegistry_FinishedTurnIsNotStopped(t *testing.T) {
	registry := newTurnRegistry()
	turn := registry.Begin("j1")
	if registry.End("j1", turn) {
		t.Fatal("a turn nobody stopped must end as finished")
	}
	if turn.ctx.Err() == nil {
		t.Fatal("End must release the turn's context")
	}
}

func TestTurnRegistry_EndingAnOlderTurnLeavesTheNewerOne(t *testing.T) {
	registry := newTurnRegistry()
	older := registry.Begin("j1")
	newer := registry.Begin("j1")

	registry.End("j1", older)
	if !registry.Stop("j1") || newer.ctx.Err() == nil {
		t.Fatal("the newer turn must still be registered and stoppable")
	}
}

func TestTurnRegistry_SameStopRequestIsClaimedOnce(t *testing.T) {
	registry := newTurnRegistry()
	if !registry.claimStopRequest("j1", "2026-10-21T20:00:00Z") {
		t.Fatal("the first delivery must be claimed")
	}
	if registry.claimStopRequest("j1", "2026-10-21T20:00:00Z") {
		t.Fatal("the same request again must not be claimed")
	}
	if !registry.claimStopRequest("j1", "2026-10-21T20:05:00Z") {
		t.Fatal("a later request for the same job is new")
	}
	for i := 0; i < 2; i++ {
		if !registry.claimStopRequest("j1", "") {
			t.Fatal("a request with no timestamp cannot be deduplicated")
		}
	}
}
