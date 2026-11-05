package log

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestWriter(t *testing.T, config RotatingWriterConfig) (*RotatingWriter, string) {
	t.Helper()
	dir := t.TempDir()
	config.LogDir = dir
	config.FilePrefix = "eksecd"
	config.Stdout = &discardWriter{}
	rw, err := NewRotatingWriter(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rw.Close() })
	return rw, dir
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func writeRotated(t *testing.T, dir, name string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return path
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestPrune_ByAgeKeepsTheCurrentFileAndSessionLogs(t *testing.T) {
	rw, dir := newTestWriter(t, RotatingWriterConfig{MaxAge: time.Hour})
	old := time.Now().Add(-2 * time.Hour)
	stale := writeRotated(t, dir, "eksecd-20260101-000000.log", old)
	fresh := writeRotated(t, dir, "eksecd-20260102-000000.log", time.Now())
	session := writeRotated(t, dir, "eksecd-session-20260101-000000.log", old)
	current := rw.GetCurrentLogPath()
	if err := os.Chtimes(current, old, old); err != nil {
		t.Fatal(err)
	}

	removed, err := rw.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed %d files, want 1", removed)
	}
	if fileExists(stale) {
		t.Error("the stale rotated file was kept")
	}
	for _, kept := range []string{fresh, session, current} {
		if !fileExists(kept) {
			t.Errorf("%s was removed", filepath.Base(kept))
		}
	}
}

func TestPrune_ByCountKeepsTheNewestFiles(t *testing.T) {
	rw, dir := newTestWriter(t, RotatingWriterConfig{MaxFiles: 3})
	now := time.Now()
	var rotated []string
	for _, stamp := range []string{"20260101-000000", "20260102-000000", "20260103-000000", "20260104-000000"} {
		rotated = append(rotated, writeRotated(t, dir, "eksecd-"+stamp+".log", now))
	}

	removed, err := rw.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Errorf("removed %d files, want 2", removed)
	}
	for _, gone := range rotated[:2] {
		if fileExists(gone) {
			t.Errorf("%s was kept", filepath.Base(gone))
		}
	}
	for _, kept := range append(rotated[2:], rw.GetCurrentLogPath()) {
		if !fileExists(kept) {
			t.Errorf("%s was removed", filepath.Base(kept))
		}
	}
}

func TestPrune_NoLimitsRemovesNothing(t *testing.T) {
	rw, dir := newTestWriter(t, RotatingWriterConfig{})
	stale := writeRotated(t, dir, "eksecd-20260101-000000.log", time.Now().Add(-365*24*time.Hour))

	removed, err := rw.Prune()
	if err != nil || removed != 0 || !fileExists(stale) {
		t.Errorf("got removed=%d err=%v exists=%v, want nothing removed", removed, err, fileExists(stale))
	}
}
