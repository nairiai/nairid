package clients

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRunCommandStreaming_Success(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "echo", "hello\nworld")

	var lines []string
	output, err := RunCommandStreaming(context.Background(), cmd, func(line []byte) {
		lines = append(lines, string(line))
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "hello") {
		t.Errorf("expected output to contain 'hello', got: %q", output)
	}
	if len(lines) == 0 {
		t.Error("expected at least one callback invocation")
	}
}

func TestRunCommandStreaming_MultipleLines(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "printf", "line1\nline2\nline3\n")

	var lines []string
	output, err := RunCommandStreaming(context.Background(), cmd, func(line []byte) {
		lines = append(lines, string(line))
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 3 {
		t.Errorf("expected 3 callback invocations, got %d: %v", len(lines), lines)
	}
	if output != "line1\nline2\nline3" {
		t.Errorf("unexpected full output: %q", output)
	}
}

func TestRunCommandStreaming_NonZeroExit(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "echo partial output; exit 1")

	var lines []string
	output, err := RunCommandStreaming(context.Background(), cmd, func(line []byte) {
		lines = append(lines, string(line))
	})

	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	cmdErr, ok := err.(*CommandError)
	if !ok {
		t.Fatalf("expected *CommandError, got %T", err)
	}
	if !strings.Contains(cmdErr.Output, "partial output") {
		t.Errorf("expected CommandError.Output to contain partial output, got: %q", cmdErr.Output)
	}
	if output != "" {
		t.Errorf("expected empty output on error, got: %q", output)
	}
	if len(lines) != 1 {
		t.Errorf("expected 1 callback invocation before failure, got %d", len(lines))
	}
}

func TestRunCommandStreaming_EmptyLinesSkipped(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "printf", "a\n\n\nb\n")

	var lines []string
	_, err := RunCommandStreaming(context.Background(), cmd, func(line []byte) {
		lines = append(lines, string(line))
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 2 {
		t.Errorf("expected 2 callbacks (empty lines skipped), got %d: %v", len(lines), lines)
	}
}

func TestRunCommandStreaming_NilCallback(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "echo", "hello")

	output, err := RunCommandStreaming(context.Background(), cmd, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "hello") {
		t.Errorf("expected output to contain 'hello', got: %q", output)
	}
}

func TestRunCommandStreaming_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	cmd := exec.CommandContext(ctx, "sleep", "10")
	_, err := RunCommandStreaming(ctx, cmd, nil)

	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestRunCommandStreaming_TimeoutKillsProcessTree(t *testing.T) {
	// Simulate the actual bug: a parent process spawns a child that hangs.
	// Without process group kill + WaitDelay, RunCommandStreaming blocks forever
	// because the child keeps stdout open.
	original := os.Getenv("AGENT_EXEC_USER")
	defer func() { _ = os.Setenv("AGENT_EXEC_USER", original) }()
	_ = os.Unsetenv("AGENT_EXEC_USER")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// sh spawns a background child (sleep 300) that holds stdout open,
	// then the parent also hangs. This mimics the opencode hang scenario.
	cmd := BuildAgentCommandWithContext(ctx, "sh", "-c", "sleep 300 & wait")

	start := time.Now()
	_, err := RunCommandStreaming(ctx, cmd, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from timeout")
	}

	// Should complete within a reasonable time (timeout + WaitDelay + margin).
	// If process group kill is broken, this would block for 300 seconds.
	maxExpected := 500*time.Millisecond + WaitDelayAfterKill + 5*time.Second
	if elapsed > maxExpected {
		t.Errorf("RunCommandStreaming took %v, expected under %v (process group kill may not be working)", elapsed, maxExpected)
	}
}
