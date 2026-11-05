package cursor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCursorService_CleanupOldLogs_PrunesEveryCLIsSessionLogs(t *testing.T) {
	logDir := t.TempDir()
	service := NewCursorService(nil, logDir, "")
	old := time.Now().AddDate(0, 0, -10)
	for _, name := range []string{"cursor-session-old.log", "claude-session-old.log", "other.log"} {
		path := filepath.Join(logDir, name)
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(logDir, "cursor-session-new.log"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := service.CleanupOldLogs(7); err != nil {
		t.Fatal(err)
	}

	for _, gone := range []string{"cursor-session-old.log", "claude-session-old.log"} {
		if _, err := os.Stat(filepath.Join(logDir, gone)); err == nil {
			t.Errorf("%s was kept", gone)
		}
	}
	for _, kept := range []string{"cursor-session-new.log", "other.log"} {
		if _, err := os.Stat(filepath.Join(logDir, kept)); err != nil {
			t.Errorf("%s was removed", kept)
		}
	}
}

func TestCursorService_CleanupOldLogs_InvalidMaxAge(t *testing.T) {
	service := NewCursorService(nil, t.TempDir(), "")
	for _, maxAge := range []int{0, -1} {
		if err := service.CleanupOldLogs(maxAge); err == nil {
			t.Errorf("maxAgeDays=%d returned no error", maxAge)
		}
	}
}
