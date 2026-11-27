package opencode

import (
	"context"
	"strings"
	"testing"

	"nairid/clients"
	"nairid/internal/testsupport"
)

func TestStartNewSession_LogsTheArgvWithoutThePrompt(t *testing.T) {
	testsupport.InstallFakeCLI(t, "opencode")
	logs := testsupport.CaptureLog(t)
	const prompt = "rotate the acme staging password"

	_, err := NewOpenCodeClient().StartNewSession(context.Background(), prompt, &clients.OpenCodeOptions{Model: "openai/gpt-5.5"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := logs.String()
	if strings.Contains(got, prompt) {
		t.Errorf("the log contains the prompt:\n%s", got)
	}
	if !strings.Contains(got, "--model openai/gpt-5.5] (prompt 32 chars)") {
		t.Errorf("the log lacks the argv line:\n%s", got)
	}
}
