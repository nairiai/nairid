package cursor

import (
	"context"
	"strings"
	"testing"

	"nairid/clients"
	"nairid/internal/testsupport"
)

func TestStartNewSession_LogsTheArgvWithoutThePrompt(t *testing.T) {
	testsupport.InstallFakeCLI(t, "cursor-agent")
	logs := testsupport.CaptureLog(t)
	const prompt = "rotate the acme staging password"

	_, err := NewCursorClient().StartNewSession(context.Background(), prompt, &clients.CursorOptions{SystemPrompt: "be brief"})
	if err != nil {
		t.Fatal(err)
	}

	got := logs.String()
	if strings.Contains(got, prompt) || strings.Contains(got, "be brief") {
		t.Errorf("the log contains the prompt:\n%s", got)
	}
	if !strings.Contains(got, "Cursor command: [--force --print --output-format stream-json] (prompt ") {
		t.Errorf("the log lacks the argv line:\n%s", got)
	}
}
