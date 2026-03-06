package clients

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// CommandError wraps a command execution error with its captured output.
// Client packages convert this to core.ErrClaudeCommandErr.
type CommandError struct {
	Err    error
	Output string
}

func (e *CommandError) Error() string {
	return e.Err.Error()
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

// RunCommandStreaming executes a command, reading stdout line-by-line and calling onLine
// for each non-empty line. It accumulates the full output and returns it on success.
// This replaces cmd.CombinedOutput() to enable real-time progress streaming.
func RunCommandStreaming(ctx context.Context, cmd *exec.Cmd, onLine ProgressCallback) (string, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start command: %w", err)
	}

	var fullOutput strings.Builder
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			fullOutput.Write(line)
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 && onLine != nil {
				onLine(trimmed)
			}
		}
		if err != nil {
			break
		}
	}

	if err := cmd.Wait(); err != nil {
		return "", &CommandError{
			Err:    err,
			Output: fullOutput.String(),
		}
	}

	return strings.TrimSpace(fullOutput.String()), nil
}
