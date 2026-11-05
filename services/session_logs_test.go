package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneSessionLogs_CoversEveryCLI(t *testing.T) {
	logDir := t.TempDir()
	old := time.Now().AddDate(0, 0, -31)
	stale := []string{"claude-session-a.log", "codex-session-a.log", "cursor-session-a.log", "opencode-session-a.log"}
	for _, name := range append(stale, "eksecd-20260101-000000.log") {
		path := filepath.Join(logDir, name)
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	if err := PruneSessionLogs(logDir, 30); err != nil {
		t.Fatal(err)
	}

	for _, name := range stale {
		if _, err := os.Stat(filepath.Join(logDir, name)); err == nil {
			t.Errorf("%s was kept", name)
		}
	}
	if _, err := os.Stat(filepath.Join(logDir, "eksecd-20260101-000000.log")); err != nil {
		t.Error("the program log was removed")
	}
	if err := PruneSessionLogs(logDir, 0); err == nil {
		t.Error("maxAgeDays=0 returned no error")
	}
}
