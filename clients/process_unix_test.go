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

func TestStopLadder_InterruptEndsAWellBehavedProcess(t *testing.T) {
	shortStopLadder(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := BuildAgentCommandWithContext(ctx, "sleep", "30")
	time.AfterFunc(100*time.Millisecond, cancel)

	start := time.Now()
	_, err := RunCommandStreaming(ctx, cmd, nil)
	if err == nil {
		t.Fatal("a cancelled run must not count as finished")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %s, SIGINT alone should have ended sleep", elapsed)
	}
}

func TestStopLadder_EscalatesWhenInterruptIsIgnored(t *testing.T) {
	shortStopLadder(t)
	ctx, cancel := context.WithCancel(context.Background())
	script := `trap 'echo got-int' INT; trap 'echo got-term; exit 0' TERM; while :; do sleep 0.05; done`
	cmd := BuildAgentCommandWithContext(ctx, "sh", "-c", script)
	time.AfterFunc(200*time.Millisecond, cancel)

	start := time.Now()
	_, err := RunCommandStreaming(ctx, cmd, nil)
	elapsed := time.Since(start)
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
	if elapsed < 150*time.Millisecond || elapsed > 3*time.Second {
		t.Errorf("took %s, want SIGTERM about 150ms after SIGINT", elapsed)
	}
}

func TestStopLadder_CancelledContextReportsCancelNotTimeout(t *testing.T) {
	shortStopLadder(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := BuildAgentCommandWithContext(ctx, "sleep", "30")
	cancel()
	_, err := RunCommandStreaming(ctx, cmd, nil)
	if err == nil || ctx.Err() != context.Canceled {
		t.Fatalf("err %v, ctx %v", err, ctx.Err())
	}
}
