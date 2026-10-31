package claude

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

const lastProgressNote = "Now I'll loop on the review findings until the reviewer is clean."

// killedMidTask is the stream of a run nairid killed at its deadline: progress, no result.
const killedMidTask = `{"type":"system","subtype":"init","session_id":"sess_1"}
{"type":"assistant","message":{"id":"msg_1","type":"message","content":[{"type":"text","text":"` + lastProgressNote + `"}],"stop_reason":"end_turn"},"session_id":"sess_1"}`

func killedAtDeadline(output string) error {
	return &core.ErrClaudeCommandErr{
		Err:    &core.ErrSessionTimeout{Timeout: 2 * time.Hour, Err: errors.New("signal: killed")},
		Output: output,
	}
}

func assertSessionTimeout(t *testing.T, err error) {
	t.Helper()
	if _, ok := core.IsSessionTimeout(err); !ok {
		t.Fatalf("got %v, want a session timeout", err)
	}
	if strings.Contains(err.Error(), lastProgressNote) {
		t.Errorf("the error quotes the agent's last progress note: %v", err)
	}
}

func TestClaudeService_ASessionTimeoutIsNotTheLastProgressNote(t *testing.T) {
	var timeouts []time.Duration
	mock := &services.MockClaudeClient{
		StartNewSessionFunc: func(_ string, options *clients.ClaudeOptions) (string, error) {
			timeouts = append(timeouts, options.SessionTimeout)
			return "", killedAtDeadline(killedMidTask)
		},
		ContinueSessionFunc: func(_, _ string, options *clients.ClaudeOptions) (string, error) {
			timeouts = append(timeouts, options.SessionTimeout)
			return "", killedAtDeadline(killedMidTask)
		},
	}
	service := NewClaudeService(mock, t.TempDir(), "", nil, nil)

	result, err := service.StartNewConversationWithProgress(context.Background(), "ship it", "", "", "", 2*time.Hour, nil)
	if result != nil {
		t.Errorf("the run cut off at its deadline was reported as done: %+v", result)
	}
	assertSessionTimeout(t, err)

	result, err = service.ContinueConversationWithProgress(context.Background(), "sess_1", "go on", "", "", "", 3*time.Hour, nil)
	if result != nil {
		t.Errorf("the run cut off at its deadline was reported as done: %+v", result)
	}
	assertSessionTimeout(t, err)

	if len(timeouts) != 2 || timeouts[0] != 2*time.Hour || timeouts[1] != 3*time.Hour {
		t.Errorf("the CLI ran with timeouts %v, want [2h 3h]", timeouts)
	}
}

// The CLI wrote its result before the kill, so the run did finish.
func TestClaudeService_AResultWrittenBeforeTheDeadlineIsKept(t *testing.T) {
	service := NewClaudeService(nil, t.TempDir(), "", nil, nil)
	output := killedMidTask + `
{"type":"result","subtype":"success","is_error":false,"result":"All review threads resolved.","session_id":"sess_1"}`

	err := service.handleClaudeClientError(killedAtDeadline(output), "failed to continue Claude session")

	success, ok := core.IsClaudeCLISuccessfulResponse(err)
	if !ok || success.Result != "All review threads resolved." {
		t.Errorf("got %v, want the result the CLI wrote", err)
	}
}
