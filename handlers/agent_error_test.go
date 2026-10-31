package handlers

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"nairid/core"
	"nairid/models"
)

func systemMessages(box *outbox) []string {
	var msgs []string
	for _, msg := range box.ofType(models.MessageTypeSystemMessage) {
		payload, _ := msg.Payload.(models.SystemMessagePayload)
		msgs = append(msgs, payload.Message)
	}
	return msgs
}

// waitForSystemMessage returns the first system message containing want, failing after 5s.
func waitForSystemMessage(t *testing.T, box *outbox, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var seen []string
	for time.Now().Before(deadline) {
		seen = systemMessages(box)
		if i := slices.IndexFunc(seen, func(m string) bool { return strings.Contains(m, want) }); i >= 0 {
			return seen[i]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no system message contains %q; got %q", want, seen)
	return ""
}

func codexCommandFailure(operation string) error {
	return fmt.Errorf("%s: %w", operation,
		&core.ErrClaudeCommandErr{Agent: "Codex", Err: errors.New("exit status 1"), Output: "model not supported"})
}

func TestFailedTurn_NamesTheAgentCLIThatFailed(t *testing.T) {
	t.Run("start", func(t *testing.T) {
		f := newStopFixture(t)
		f.agent.name = "codex"
		f.agent.failWith = codexCommandFailure("failed to start new Codex session")

		f.handler.HandleMessage(startMessage("0"))

		got := waitForSystemMessage(t, f.outbox, "error starting codex session: ")
		if !strings.Contains(got, "Codex command failed: exit status 1") || strings.Contains(strings.ToLower(got), "claude") {
			t.Errorf("system message = %q", got)
		}
	})

	t.Run("continue", func(t *testing.T) {
		f := newStopFixture(t)
		f.agent.name = "codex"
		f.mustHandle(t, startMessage("0"))
		f.agent.failWith = codexCommandFailure("failed to continue Codex session")

		f.handler.HandleMessage(replyMessage("1"))

		got := waitForSystemMessage(t, f.outbox, "error continuing codex session: ")
		if !strings.Contains(got, "Codex command failed: exit status 1") || strings.Contains(strings.ToLower(got), "claude") {
			t.Errorf("system message = %q", got)
		}
	})
}
