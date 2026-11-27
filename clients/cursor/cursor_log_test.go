package cursor

import (
	"context"
	"os"
	"path/filepath"
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

func TestStartNewSession_AFailedCallKeepsThePromptOutOfTheError(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
for last; do :; done
printf 'warning: slow network{"type":"user","message":{"content":[{"type":"text","text":"%s"}]}}\n' "$last"
printf '{"type":"assistant","message":"partial reply"}\n'
exit 1
`
	if err := os.WriteFile(filepath.Join(dir, "cursor-agent"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	const prompt = "rotate the acme staging password"

	_, err := NewCursorClient().StartNewSession(context.Background(), prompt, nil)
	if err == nil {
		t.Fatal("the failing CLI returned no error")
	}
	if strings.Contains(err.Error(), prompt) {
		t.Errorf("the error carries the prompt: %v", err)
	}
	if !strings.Contains(err.Error(), "partial reply") {
		t.Errorf("the error lost the rest of the output: %v", err)
	}
}
