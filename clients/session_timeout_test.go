package clients

import (
	"testing"
	"time"
)

func TestResolveSessionTimeout(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		agentMs int64
		want    time.Duration
	}{
		{"no agent value and no env runs for 1h", "", 0, time.Hour},
		{"no agent value uses NAIRI_MAX_SESSION_MS", "1800000", 0, 30 * time.Minute},
		{"a negative agent value counts as none", "1800000", -5, 30 * time.Minute},
		{"the agent value beats NAIRI_MAX_SESSION_MS", "1800000", (2 * time.Hour).Milliseconds(), 2 * time.Hour},
		{"below 10m is raised to 10m", "", time.Minute.Milliseconds(), MinAgentSessionTimeout},
		{"above 24h is lowered to 24h", "", (48 * time.Hour).Milliseconds(), MaxAgentSessionTimeout},
		{"10m is kept", "", (10 * time.Minute).Milliseconds(), 10 * time.Minute},
		{"24h is kept", "", (24 * time.Hour).Milliseconds(), 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NAIRI_MAX_SESSION_MS", tt.env)
			t.Setenv("EKSEC_MAX_SESSION_MS", "")
			if got := ResolveSessionTimeout(tt.agentMs); got != tt.want {
				t.Errorf("ResolveSessionTimeout(%d) = %s, want %s", tt.agentMs, got, tt.want)
			}
		})
	}
}

func TestRunTimeout(t *testing.T) {
	t.Setenv("NAIRI_MAX_SESSION_MS", "1800000")
	t.Setenv("EKSEC_MAX_SESSION_MS", "")

	tests := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"nil Claude options", (*ClaudeOptions)(nil).RunTimeout(), 30 * time.Minute},
		{"Claude options without a timeout", (&ClaudeOptions{}).RunTimeout(), 30 * time.Minute},
		{"Claude options with a timeout", (&ClaudeOptions{SessionTimeout: 2 * time.Hour}).RunTimeout(), 2 * time.Hour},
		{"nil Codex options", (*CodexOptions)(nil).RunTimeout(), 30 * time.Minute},
		{"Codex options with a timeout", (&CodexOptions{SessionTimeout: 2 * time.Hour}).RunTimeout(), 2 * time.Hour},
		{"nil OpenCode options", (*OpenCodeOptions)(nil).RunTimeout(), 30 * time.Minute},
		{"OpenCode options with a timeout", (&OpenCodeOptions{SessionTimeout: 2 * time.Hour}).RunTimeout(), 2 * time.Hour},
		{"nil Cursor options", (*CursorOptions)(nil).RunTimeout(), 30 * time.Minute},
		{"Cursor options with a timeout", (&CursorOptions{SessionTimeout: 2 * time.Hour}).RunTimeout(), 2 * time.Hour},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: RunTimeout() = %s, want %s", tt.name, tt.got, tt.want)
		}
	}
}
