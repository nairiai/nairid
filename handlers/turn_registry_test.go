package handlers

import "testing"

func TestTurnRegistry_StopEndsTheRunningTurn(t *testing.T) {
	registry := newTurnRegistry()
	ctx := registry.Begin("j1", "cmsg_1")

	if _, running := registry.Stop("j2", "U1"); running {
		t.Fatal("a job with no turn must not report a running turn")
	}
	processedMessageID, running := registry.Stop("j1", "U1")
	if !running || processedMessageID != "cmsg_1" {
		t.Fatalf("Stop = (%q, %v), want (cmsg_1, true)", processedMessageID, running)
	}
	if ctx.Err() == nil {
		t.Fatal("the turn's context must be cancelled by Stop")
	}
	stopped, by := registry.End("j1")
	if !stopped || by != "U1" {
		t.Fatalf("End = (%v, %q), want (true, U1)", stopped, by)
	}
	if stopped, _ := registry.End("j1"); stopped {
		t.Fatal("a second End must be a no-op")
	}
}

func TestTurnRegistry_FinishedTurnIsNotStopped(t *testing.T) {
	registry := newTurnRegistry()
	registry.Begin("j1", "cmsg_1")
	if stopped, _ := registry.End("j1"); stopped {
		t.Fatal("a turn nobody stopped must end as finished")
	}
	if _, running := registry.Stop("j1", "U1"); running {
		t.Fatal("a finished turn is not running")
	}
}

func TestTurnRegistry_BeginReplacesALeftoverTurn(t *testing.T) {
	registry := newTurnRegistry()
	leftover := registry.Begin("j1", "cmsg_1")

	ctx := registry.Begin("j1", "cmsg_2")
	if leftover.Err() == nil {
		t.Fatal("the leftover turn must be cancelled")
	}
	if ctx.Err() != nil {
		t.Fatal("the new turn must start running")
	}
	if id, running := registry.Stop("j1", "U1"); !running || id != "cmsg_2" {
		t.Fatalf("Stop = (%q, %v), want the new turn", id, running)
	}
}

func TestTurnRegistry_SameStopRequestIsSeenOnce(t *testing.T) {
	registry := newTurnRegistry()
	if registry.recordStopRequest("j1", "2026-10-21T20:00:00Z") {
		t.Fatal("first sight")
	}
	if !registry.recordStopRequest("j1", "2026-10-21T20:00:00Z") {
		t.Fatal("the same request again must not count")
	}
	if registry.recordStopRequest("j1", "2026-10-21T20:05:00Z") {
		t.Fatal("a later request for the same job is new")
	}
	for i := 0; i < 2; i++ {
		if registry.recordStopRequest("j1", "") {
			t.Fatal("a request with no timestamp cannot be deduplicated")
		}
	}
}
