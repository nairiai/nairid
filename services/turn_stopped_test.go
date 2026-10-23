package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"nairid/core"
)

func TestTurnStoppedError_OnlyACancelIsAStop(t *testing.T) {
	sessionFromOutput := func(output string) string { return "session-" + output }
	runErr := &core.ErrClaudeCommandErr{Err: errors.New("signal: interrupt"), Output: "partial"}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	stopped, ok := TurnStoppedError(cancelled, runErr, sessionFromOutput)
	if !ok || stopped.SessionID != "session-partial" {
		t.Fatalf("cancelled run = (%+v, %v), want a stop carrying session-partial", stopped, ok)
	}

	timedOut, cancelTimeout := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelTimeout()
	<-timedOut.Done()
	if _, ok := TurnStoppedError(timedOut, runErr, sessionFromOutput); ok {
		t.Error("a run that hit its timeout must be reported as a failure, not a stop")
	}

	if _, ok := TurnStoppedError(cancelled, nil, sessionFromOutput); ok {
		t.Error("a run that returned no error is not a stop")
	}
}
