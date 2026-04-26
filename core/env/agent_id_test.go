package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withClearedAgentEnv saves and clears NAIRI_AGENT_ID and EKSEC_AGENT_ID for a
// test, restoring them on cleanup. Returns a setter the test can use to set a
// specific value during the test body.
func withClearedAgentEnv(t *testing.T) func(key, val string) {
	t.Helper()
	saved := map[string]string{}
	for _, key := range []string{"NAIRI_AGENT_ID", "EKSEC_AGENT_ID"} {
		saved[key] = os.Getenv(key)
		_ = os.Unsetenv(key)
	}
	t.Cleanup(func() {
		for key, val := range saved {
			if val == "" {
				_ = os.Unsetenv(key)
			} else {
				_ = os.Setenv(key, val)
			}
		}
	})
	return func(key, val string) {
		if val == "" {
			_ = os.Unsetenv(key)
		} else {
			_ = os.Setenv(key, val)
		}
	}
}

func TestGetAgentID_PrefersNairiOverLegacy(t *testing.T) {
	setEnv := withClearedAgentEnv(t)
	setEnv("NAIRI_AGENT_ID", "my-nairi-agent")
	setEnv("EKSEC_AGENT_ID", "my-legacy-agent")

	got, err := GetAgentID()
	if err != nil {
		t.Fatalf("GetAgentID returned error: %v", err)
	}
	if got != "my-nairi-agent" {
		t.Errorf("expected 'my-nairi-agent', got %q", got)
	}
}

func TestGetAgentID_FallsBackToLegacy(t *testing.T) {
	setEnv := withClearedAgentEnv(t)
	setEnv("EKSEC_AGENT_ID", "legacy-only")

	got, err := GetAgentID()
	if err != nil {
		t.Fatalf("GetAgentID returned error: %v", err)
	}
	if got != "legacy-only" {
		t.Errorf("expected 'legacy-only', got %q", got)
	}
}

func TestGetAgentID_ErrorsWhenUnset(t *testing.T) {
	withClearedAgentEnv(t)

	_, err := GetAgentID()
	if err == nil {
		t.Fatal("expected error when both NAIRI_AGENT_ID and EKSEC_AGENT_ID are unset")
	}
	if !strings.Contains(err.Error(), "NAIRI_AGENT_ID") {
		t.Errorf("expected error to mention NAIRI_AGENT_ID, got: %v", err)
	}
}

// withTempConfigDir points NAIRI_CONFIG_DIR at a temp dir for the duration of
// the test, so the env-helpers don't poke at the user's real ~/.config/eksecd.
func withTempConfigDir(t *testing.T) string {
	t.Helper()
	tempDir := t.TempDir()
	saved := os.Getenv("NAIRI_CONFIG_DIR")
	_ = os.Setenv("NAIRI_CONFIG_DIR", tempDir)
	t.Cleanup(func() {
		if saved == "" {
			_ = os.Unsetenv("NAIRI_CONFIG_DIR")
		} else {
			_ = os.Setenv("NAIRI_CONFIG_DIR", saved)
		}
	})
	return tempDir
}

func TestGetAgentStatePath_Namespaced(t *testing.T) {
	tempDir := withTempConfigDir(t)

	got, err := GetAgentStatePath("agent-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(tempDir, "agents", "agent-abc", "state.json")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
	// Parent dir must exist (caller writes state.json into it later).
	if _, err := os.Stat(filepath.Dir(got)); err != nil {
		t.Errorf("expected parent dir to exist, stat err: %v", err)
	}
}

func TestGetAgentStatePath_RejectsEmptyAgentID(t *testing.T) {
	withTempConfigDir(t)
	if _, err := GetAgentStatePath(""); err == nil {
		t.Error("expected error for empty agent ID")
	}
}

func TestGetAgentStatePath_DistinctAgentsGetDistinctPaths(t *testing.T) {
	withTempConfigDir(t)

	a, err := GetAgentStatePath("agent-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := GetAgentStatePath("agent-B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == b {
		t.Errorf("expected distinct state paths for distinct agents, got %q for both", a)
	}
}

func TestGetAgentLogsDir_Namespaced(t *testing.T) {
	tempDir := withTempConfigDir(t)

	got, err := GetAgentLogsDir("agent-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(tempDir, "logs", "agent-xyz")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("expected logs dir to exist, stat err: %v", err)
	}
}

func TestGetAgentLogsDir_RejectsEmptyAgentID(t *testing.T) {
	withTempConfigDir(t)
	if _, err := GetAgentLogsDir(""); err == nil {
		t.Error("expected error for empty agent ID")
	}
}

func TestGetAgentWorktreeBasePath_DefaultMode(t *testing.T) {
	saved := os.Getenv("AGENT_EXEC_USER")
	_ = os.Unsetenv("AGENT_EXEC_USER")
	t.Cleanup(func() {
		if saved == "" {
			_ = os.Unsetenv("AGENT_EXEC_USER")
		} else {
			_ = os.Setenv("AGENT_EXEC_USER", saved)
		}
	})

	got, err := GetAgentWorktreeBasePath("agent-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	homeDir, _ := os.UserHomeDir()
	want := filepath.Join(homeDir, ".eksec_worktrees", "agent-agent-1")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestGetAgentWorktreeBasePath_ManagedMode(t *testing.T) {
	saved := os.Getenv("AGENT_EXEC_USER")
	_ = os.Setenv("AGENT_EXEC_USER", "ccagent")
	t.Cleanup(func() {
		if saved == "" {
			_ = os.Unsetenv("AGENT_EXEC_USER")
		} else {
			_ = os.Setenv("AGENT_EXEC_USER", saved)
		}
	})

	got, err := GetAgentWorktreeBasePath("agent-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join("/home", "ccagent", ".eksec_worktrees", "agent-agent-2")
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestGetAgentWorktreeBasePath_DistinctAgentsGetDistinctPaths(t *testing.T) {
	a, err := GetAgentWorktreeBasePath("agent-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := GetAgentWorktreeBasePath("agent-B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == b {
		t.Errorf("expected distinct worktree paths for distinct agents, got %q for both", a)
	}
	// Crucial property for the issue #201 fix: scans of one agent's subtree
	// must not see the other agent's subtree.
	if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) {
		t.Errorf("agent paths must not be prefixes of each other: %q, %q", a, b)
	}
}

func TestGetAgentWorktreeBasePath_RejectsEmptyAgentID(t *testing.T) {
	if _, err := GetAgentWorktreeBasePath(""); err == nil {
		t.Error("expected error for empty agent ID")
	}
}
