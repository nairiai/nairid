//go:build !windows

package clients

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
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

func TestStopLadder_InterruptAloneEndsAProcessThatHandlesIt(t *testing.T) {
	shortStopLadder(t)
	ctx, cancel := context.WithCancel(context.Background())
	script := `trap 'echo got-int; exit 0' INT; trap 'echo got-term; exit 0' TERM; while :; do sleep 0.05; done`
	cmd := BuildAgentCommandWithContext(ctx, "sh", "-c", script)
	time.AfterFunc(200*time.Millisecond, cancel)

	_, err := RunCommandStreaming(ctx, cmd, nil)
	cmdErr, ok := err.(*CommandError)
	if !ok {
		t.Fatalf("got %v (%T), want *CommandError for a cancelled run", err, err)
	}
	if !strings.Contains(cmdErr.Output, "got-int") || strings.Contains(cmdErr.Output, "got-term") {
		t.Errorf("the process should have ended on SIGINT alone, output: %q", cmdErr.Output)
	}
}

func TestStopLadder_EscalatesWhenInterruptIsIgnored(t *testing.T) {
	shortStopLadder(t)
	ctx, cancel := context.WithCancel(context.Background())
	script := `trap 'echo got-int' INT; trap 'echo got-term; exit 0' TERM; while :; do sleep 0.05; done`
	cmd := BuildAgentCommandWithContext(ctx, "sh", "-c", script)
	cancelledAt := make(chan time.Time, 1)
	time.AfterFunc(200*time.Millisecond, func() {
		cancelledAt <- time.Now()
		cancel()
	})

	_, err := RunCommandStreaming(ctx, cmd, nil)
	sinceCancel := time.Since(<-cancelledAt)
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
	if sinceCancel < 150*time.Millisecond || sinceCancel > 3*time.Second {
		t.Errorf("ended %s after the cancel, want SIGTERM about 150ms after SIGINT", sinceCancel)
	}
}

// Values whose bytes bash could change, end a command on, or fail to parse.
var envValueCases = []struct {
	name  string
	value string
}{
	{"space", "hello world"},
	{"ampersand", "http://x?a=1&b=2"},
	{"semicolon", "x;y"},
	{"double dollar", "pa$$word"},
	{"variable", "$HOME/x"},
	{"dollar paren", "$(echo RAN)"},
	{"single quote", "it's"},
	{"only single quotes", "''"},
	{"backtick", "a`echo RAN`b"},
	{"tilde", "~/x"},
	{"glob", "*"},
	{"newline", "line one\nline two\n"},
	{"double quotes", `"quoted"`},
	{"backslash", `a\b\`},
	{"hash", "# not a comment"},
	{"pipe and redirect", "a|b>c"},
	{"equals", "a=b=c"},
	{"empty", ""},
}

// Runs the real managed script through bash, skipping only the sudo in front of it.
func runManagedScript(t *testing.T, name string, args ...string) string {
	t.Helper()
	t.Setenv("AGENT_EXEC_USER", "agentrunner")
	script := BuildAgentCommandWithContext(context.Background(), name, args...).Args[5]

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("bash", "-c", script)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("script failed: %v, stderr: %q", err, stderr.String())
	}
	if stderr.Len() > 0 {
		t.Errorf("unexpected stderr: %q", stderr.String())
	}
	return stdout.String()
}

func TestManagedCommand_ChildSeesEnvValueUnchanged(t *testing.T) {
	for _, tt := range envValueCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ENV_VALUE_UNDER_TEST", tt.value)

			got := runManagedScript(t, "bash", "-c", `printf %s "${ENV_VALUE_UNDER_TEST-unset}"`)
			if got != tt.value {
				t.Errorf("child saw %q, want %q", got, tt.value)
			}
		})
	}
}

func TestManagedCommand_AllEnvValuesTogetherKeepUmaskAndStdout(t *testing.T) {
	want := []string{"0002"}
	childArgs := []string{`"$(umask)"`}
	for i, tt := range envValueCases {
		key := "ENV_VALUE_UNDER_TEST_" + strconv.Itoa(i)
		t.Setenv(key, tt.value)
		want = append(want, tt.value)
		childArgs = append(childArgs, `"${`+key+`-unset}"`)
	}

	got := runManagedScript(t, "bash", "-c", `printf '%s\0' `+strings.Join(childArgs, " "))
	wantOutput := strings.Join(want, "\x00") + "\x00"
	if got != wantOutput {
		t.Errorf("child saw %q, want %q", got, wantOutput)
	}
}

func TestManagedCommand_BlockedEnvVarStaysHidden(t *testing.T) {
	t.Setenv("NAIRI_API_KEY", "it's blocked; still")

	got := runManagedScript(t, "bash", "-c", `printf %s "${NAIRI_API_KEY-unset}"`)
	if got != "unset" {
		t.Errorf("child saw %q, want the variable unset", got)
	}
}
