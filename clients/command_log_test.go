package clients

import "testing"

func TestArgsForLog_LeavesThePromptOut(t *testing.T) {
	got := ArgsForLog([]string{"exec", "--json", "fix the login bug"}, "fix the login bug")
	if want := "[exec --json] (prompt 17 chars)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
