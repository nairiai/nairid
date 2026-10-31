package cursor

import (
	"errors"
	"strings"
	"testing"
	"time"

	"nairid/core"
)

func TestCursorService_ASessionTimeoutIsNotTheLastProgressNote(t *testing.T) {
	const note = "Now I'll loop on the review findings until the reviewer is clean."
	service := NewCursorService(nil, t.TempDir(), "")
	killed := &core.ErrClaudeCommandErr{
		Err:    &core.ErrSessionTimeout{Timeout: 2 * time.Hour, Err: errors.New("signal: killed")},
		Output: `{"type":"result","result":"` + note + `","session_id":"s1"}`,
	}

	err := service.handleCursorClientError(killed, "failed to continue Cursor session")

	timeoutErr, ok := core.IsSessionTimeout(err)
	if !ok {
		t.Fatalf("got %v, want a session timeout", err)
	}
	if timeoutErr.SessionID != "s1" {
		t.Errorf("the timeout kept session %q, want s1 so a reply resumes it", timeoutErr.SessionID)
	}
	if strings.Contains(err.Error(), note) {
		t.Errorf("the error quotes the agent's last progress note: %v", err)
	}
}
