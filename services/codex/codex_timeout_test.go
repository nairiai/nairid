package codex

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nairid/clients"
	"nairid/core"
	"nairid/services"
)

func TestCodexService_ASessionTimeoutIsNotTheLastProgressNote(t *testing.T) {
	const note = "Now I'll loop on the review findings until the reviewer is clean."
	killed := &core.ErrClaudeCommandErr{
		Err: &core.ErrSessionTimeout{Timeout: 2 * time.Hour, Err: errors.New("signal: killed")},
		Output: `{"type":"thread.started","thread_id":"thread_1"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"` + note + `"}}`,
	}
	var timeouts []time.Duration
	mock := &services.MockCodexClient{
		StartNewSessionFunc: func(_ string, options *clients.CodexOptions) (string, error) {
			timeouts = append(timeouts, options.SessionTimeout)
			return "", killed
		},
		ContinueSessionFunc: func(_, _ string, options *clients.CodexOptions) (string, error) {
			timeouts = append(timeouts, options.SessionTimeout)
			return "", killed
		},
	}
	service := NewCodexService(mock, t.TempDir(), "gpt-5.5")

	_, startErr := service.StartNewConversationWithProgress(context.Background(), "ship it", "", "", "", 2*time.Hour, nil)
	_, continueErr := service.ContinueConversationWithProgress(context.Background(), "thread_1", "go on", "", "", "", 3*time.Hour, nil)

	for _, err := range []error{startErr, continueErr} {
		timeoutErr, ok := core.IsSessionTimeout(err)
		if !ok {
			t.Errorf("got %v, want a session timeout", err)
			continue
		}
		if timeoutErr.SessionID != "thread_1" {
			t.Errorf("the timeout kept session %q, want thread_1 so a reply resumes it", timeoutErr.SessionID)
		}
		if err != nil && strings.Contains(err.Error(), note) {
			t.Errorf("the error quotes the agent's last progress note: %v", err)
		}
	}
	if len(timeouts) != 2 || timeouts[0] != 2*time.Hour || timeouts[1] != 3*time.Hour {
		t.Errorf("the CLI ran with timeouts %v, want [2h 3h]", timeouts)
	}
}
