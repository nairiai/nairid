package retention

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDays(t *testing.T) {
	tests := []struct {
		value string
		want  int
	}{
		{"", 30},
		{"30", 30},
		{"7", 7},
		{"0", 30},
		{"-1", 30},
		{"abc", 30},
	}
	for _, tt := range tests {
		t.Run("value "+tt.value, func(t *testing.T) {
			t.Setenv(EnvVar, tt.value)
			if got := Days(); got != tt.want {
				t.Errorf("Days() = %d, want %d", got, tt.want)
			}
		})
	}
}

func writeAged(t *testing.T, path string, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestPruneFiles(t *testing.T) {
	dir := t.TempDir()
	cutoff := time.Now().Add(-time.Hour)
	older := filepath.Join(dir, "claude-session-older.log")
	newer := filepath.Join(dir, "claude-session-newer.log")
	otherPrefix := filepath.Join(dir, "eksecd-older.log")
	otherSuffix := filepath.Join(dir, "claude-session-older.txt")
	skipped := filepath.Join(dir, "claude-session-skipped.log")
	inSubdir := filepath.Join(dir, "sub", "claude-session-older.log")
	writeAged(t, older, cutoff.Add(-time.Second))
	writeAged(t, newer, cutoff.Add(time.Second))
	writeAged(t, otherPrefix, cutoff.Add(-time.Second))
	writeAged(t, otherSuffix, cutoff.Add(-time.Second))
	writeAged(t, skipped, cutoff.Add(-time.Second))
	writeAged(t, inSubdir, cutoff.Add(-time.Second))

	removed, err := PruneFiles(dir, []string{"claude-session-"}, []string{".log"}, cutoff, func(name string) bool {
		return name == "claude-session-skipped.log"
	})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed %d files, want 1", removed)
	}
	if exists(older) {
		t.Error("the file older than the cutoff was kept")
	}
	for _, kept := range []string{newer, otherPrefix, otherSuffix, skipped, inSubdir} {
		if !exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}
}

func TestPruneFiles_MissingDir(t *testing.T) {
	removed, err := PruneFiles(filepath.Join(t.TempDir(), "missing"), nil, nil, time.Now(), nil)
	if err != nil || removed != 0 {
		t.Errorf("got removed=%d err=%v, want 0 and nil", removed, err)
	}
}

func TestPruneDirs(t *testing.T) {
	dir := t.TempDir()
	cutoff := time.Now().Add(-time.Hour)
	stale := filepath.Join(dir, "job_stale")
	fresh := filepath.Join(dir, "job_fresh")
	file := filepath.Join(dir, "job_file.txt")
	writeAged(t, filepath.Join(stale, "a.txt"), cutoff.Add(-time.Second))
	writeAged(t, filepath.Join(stale, "nested", "b.txt"), cutoff.Add(-time.Second))
	writeAged(t, filepath.Join(fresh, "a.txt"), cutoff.Add(-time.Second))
	writeAged(t, filepath.Join(fresh, "nested", "b.txt"), cutoff.Add(time.Second))
	writeAged(t, file, cutoff.Add(-time.Second))
	for _, path := range []string{stale, filepath.Join(stale, "nested"), fresh, filepath.Join(fresh, "nested")} {
		if err := os.Chtimes(path, cutoff.Add(-time.Second), cutoff.Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := PruneDirs(dir, cutoff, os.RemoveAll)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("removed %d dirs, want 1", removed)
	}
	if exists(stale) {
		t.Error("the stale dir was kept")
	}
	if !exists(fresh) {
		t.Error("the dir with a fresh file was removed")
	}
	if !exists(file) {
		t.Error("a plain file was removed")
	}
}

func TestPruneDirs_MissingDir(t *testing.T) {
	removed, err := PruneDirs(filepath.Join(t.TempDir(), "missing"), time.Now(), os.RemoveAll)
	if err != nil || removed != 0 {
		t.Errorf("got removed=%d err=%v, want 0 and nil", removed, err)
	}
}

func TestPruneRefusesALinkedRoot(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, "victim")
	old := time.Now().Add(-60 * 24 * time.Hour)
	for _, path := range []string{filepath.Join(victim, ".ssh", "id_ed25519"), filepath.Join(victim, "eksecd-1.log")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)

	linkedRoot := filepath.Join(base, "eksecd", "attachments")
	if err := os.MkdirAll(filepath.Dir(linkedRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, linkedRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := PruneDirs(linkedRoot, cutoff, os.RemoveAll); err == nil {
		t.Error("PruneDirs pruned through a linked root")
	}

	linkedParent := filepath.Join(base, "linked-parent")
	if err := os.Symlink(base, linkedParent); err != nil {
		t.Fatal(err)
	}
	if _, err := PruneDirs(filepath.Join(linkedParent, "victim"), cutoff, os.RemoveAll); err == nil {
		t.Error("PruneDirs pruned under a linked parent")
	}

	for _, path := range []string{filepath.Join(victim, ".ssh", "id_ed25519"), filepath.Join(victim, "eksecd-1.log")} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was removed: %v", path, err)
		}
	}
}

func TestPruneSharedDirsRefusesAParentOthersCanChange(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "eksecd")
	dir := filepath.Join(parent, "attachments")
	old := time.Now().Add(-60 * 24 * time.Hour)
	job := filepath.Join(dir, "job_1")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(job, "a.txt"), job} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	if _, err := PruneSharedDirs(dir, cutoff, os.RemoveAll); err == nil {
		t.Error("PruneSharedDirs pruned a folder whose parent anyone can change")
	}
	if _, err := os.Stat(job); err != nil {
		t.Errorf("%s was removed: %v", job, err)
	}

	// A folder in the daemon's own config dir, which a volume can make group-writable, still prunes.
	if removed, err := PruneDirs(dir, cutoff, os.RemoveAll); err != nil || removed != 1 {
		t.Errorf("PruneDirs: removed %d, err %v; want 1 and nil", removed, err)
	}
}

func TestPruneSharedDirs_MissingParent(t *testing.T) {
	removed, err := PruneSharedDirs(filepath.Join(t.TempDir(), "none", "attachments"), time.Now(), os.RemoveAll)
	if err != nil || removed != 0 {
		t.Errorf("got removed=%d err=%v, want 0 and nil", removed, err)
	}
}

func TestPruneSharedDirsRefusesALinkedParent(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, "victim")
	job := filepath.Join(victim, "attachments", "job_1")
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(job, old, old); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(base, "eksecd")
	if err := os.Symlink(victim, linkedParent); err != nil {
		t.Fatal(err)
	}

	_, err := PruneSharedDirs(filepath.Join(linkedParent, "attachments"), time.Now().Add(-30*24*time.Hour), os.RemoveAll)
	if err == nil || !strings.Contains(err.Error(), "is a symbolic link") {
		t.Errorf("err = %v, want a refusal naming the symbolic link", err)
	}
	if _, err := os.Stat(job); err != nil {
		t.Errorf("%s was removed: %v", job, err)
	}
}
