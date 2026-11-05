package utils

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneCodexRollouts(t *testing.T) {
	codexHome := t.TempDir()
	cutoff := time.Now().Add(-time.Hour)
	stale := cutoff.Add(-time.Second)
	write := func(rel string, modTime time.Time) string {
		t.Helper()
		path := filepath.Join(codexHome, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
		return path
	}
	old := write("sessions/2026/10/01/rollout-2026-10-01T10-00-00-aaaa.jsonl", stale)
	oldArchived := write("archived_sessions/rollout-2026-10-01T10-00-00-bbbb.jsonl.zst", stale)
	live := write("sessions/2026/10/01/rollout-2026-10-01T10-00-00-live-1.jsonl", stale)
	fresh := write("sessions/2026/11/05/rollout-2026-11-05T10-00-00-cccc.jsonl", cutoff.Add(time.Second))
	other := write("sessions/2026/10/01/notes.txt", stale)

	removed, err := PruneCodexRollouts(codexHome, cutoff, []string{"live-1", ""})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Errorf("removed %d files, want 2", removed)
	}
	for _, gone := range []string{old, oldArchived} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s was kept", gone)
		}
	}
	for _, kept := range []string{live, fresh, other} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed", kept)
		}
	}
}

func TestPruneCodexRollouts_MissingHome(t *testing.T) {
	removed, err := PruneCodexRollouts(filepath.Join(t.TempDir(), "none"), time.Now(), nil)
	if err != nil || removed != 0 {
		t.Errorf("got removed=%d err=%v, want 0 and nil", removed, err)
	}
}
