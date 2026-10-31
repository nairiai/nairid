package claude

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nairid/clients"
	"nairid/core"
)

func TestHandleCommandError_DeadlineIsASessionTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	err := handleCommandError(ctx, &clients.CommandError{Err: errors.New("signal: killed"), Output: "partial"}, "Claude", 2*time.Hour)

	timeoutErr, ok := core.IsSessionTimeout(err)
	if !ok {
		t.Fatalf("got %v, want a session timeout", err)
	}
	if timeoutErr.Timeout != 2*time.Hour {
		t.Errorf("timeout = %s, want 2h", timeoutErr.Timeout)
	}
	cmdErr, ok := core.IsClaudeCommandErr(err)
	if !ok || cmdErr.Output != "partial" {
		t.Errorf("the CLI output was not kept: %v", err)
	}
	if !strings.Contains(err.Error(), "session timed out after 2h0m0s") {
		t.Errorf("error = %q, want it to name the timeout", err.Error())
	}
}

func TestHandleCommandError_OtherFailuresAreNotTimeouts(t *testing.T) {
	err := handleCommandError(context.Background(), &clients.CommandError{Err: errors.New("exit status 1")}, "Claude", time.Hour)

	if _, ok := core.IsSessionTimeout(err); ok {
		t.Errorf("a plain CLI failure was reported as a timeout: %v", err)
	}
}
