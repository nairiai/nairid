//go:build !windows

package clients

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func shortStopLadder(t *testing.T) {
	t.Helper()
	interrupt, term := stopGraceAfterInterrupt, stopGraceAfterTerm
	stopGraceAfterInterrupt, stopGraceAfterTerm = 150*time.Millisecond, 150*time.Millisecond
	t.Cleanup(func() { stopGraceAfterInterrupt, stopGraceAfterTerm = interrupt, term })
	original := os.Getenv("AGENT_EXEC_USER")
	t.Cleanup(func() { _ = os.Setenv("AGENT_EXEC_USER", original) })
	_ = os.Unsetenv("AGENT_EXEC_USER")
}

func TestStopLadder_InterruptAloneEndsAProcessThatHandlesIt(t *testing.T) {
	shortStopLadder(t)
	ctx, cancel := context.WithCancel(context.Background())
	script := `trap 'echo got-int; exit 0' INT; trap 'echo got-term; exit 0' TERM; while :; do sleep 0.05; done`
	cmd := BuildAgentCommandWithContext(ctx, "sh", "-c", script)
	time.AfterFunc(200*time.Millisecond, cancel)

	_, err := RunCommandStreaming(ctx, cmd, nil)
	cmdErr, ok := err.(*CommandError)
	if !ok {
		t.Fatalf("got %v (%T), want *CommandError for a cancelled run", err, err)
	}
	if !strings.Contains(cmdErr.Output, "got-int") || strings.Contains(cmdErr.Output, "got-term") {
		t.Errorf("the process should have ended on SIGINT alone, output: %q", cmdErr.Output)
	}
}

func TestStopLadder_EscalatesWhenInterruptIsIgnored(t *testing.T) {
	shortStopLadder(t)
	ctx, cancel := context.WithCancel(context.Background())
	script := `trap 'echo got-int' INT; trap 'echo got-term; exit 0' TERM; while :; do sleep 0.05; done`
	cmd := BuildAgentCommandWithContext(ctx, "sh", "-c", script)
	cancelledAt := make(chan time.Time, 1)
	time.AfterFunc(200*time.Millisecond, func() {
		cancelledAt <- time.Now()
		cancel()
	})

	_, err := RunCommandStreaming(ctx, cmd, nil)
	sinceCancel := time.Since(<-cancelledAt)
	if err == nil {
		t.Fatal("a cancelled run must not count as finished, even on exit 0")
	}
	cmdErr, ok := err.(*CommandError)
	if !ok {
		t.Fatalf("got %T, want *CommandError", err)
	}
	if !strings.Contains(cmdErr.Output, "got-int") || !strings.Contains(cmdErr.Output, "got-term") {
		t.Errorf("the process should have seen SIGINT then SIGTERM, output: %q", cmdErr.Output)
	}
	if sinceCancel < 150*time.Millisecond || sinceCancel > 3*time.Second {
		t.Errorf("ended %s after the cancel, want SIGTERM about 150ms after SIGINT", sinceCancel)
	}
}
