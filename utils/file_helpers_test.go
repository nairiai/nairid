package utils

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Tests for readFileAsTargetUser, removeAllAsTargetUser, writeFileAsTargetUser, mkdirAllAsTargetUser
// These test the self-hosted fallback path (no AGENT_EXEC_USER set) and the path-check logic.

func TestReadFileAsTargetUser_SelfHostedMode(t *testing.T) {
	// Without AGENT_EXEC_USER set, should fall through to os.ReadFile
	_ = os.Unsetenv("AGENT_EXEC_USER")

	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.json")

	expected := []byte(`{"key": "value"}`)
	if err := os.WriteFile(filePath, expected, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	content, err := readFileAsTargetUser(filePath)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if string(content) != string(expected) {
		t.Errorf("Expected %q, got %q", string(expected), string(content))
	}
}

func TestReadFileAsTargetUser_FileNotExists(t *testing.T) {
	_ = os.Unsetenv("AGENT_EXEC_USER")

	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "nonexistent.json")

	_, err := readFileAsTargetUser(filePath)
	if err == nil {
		t.Fatal("Expected error for non-existent file")
	}

	if !os.IsNotExist(err) {
		t.Errorf("Expected os.ErrNotExist, got: %v", err)
	}
}

func TestReadFileAsTargetUser_PathNotInAgentHome(t *testing.T) {
	// When AGENT_EXEC_USER is set but path is NOT in /home/<user>, should use direct read
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "config.json")

	expected := []byte(`{"hello": "world"}`)
	if err := os.WriteFile(filePath, expected, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	_ = os.Setenv("AGENT_EXEC_USER", "agentrunner")
	defer func() { _ = os.Unsetenv("AGENT_EXEC_USER") }()

	content, err := readFileAsTargetUser(filePath)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if string(content) != string(expected) {
		t.Errorf("Expected %q, got %q", string(expected), string(content))
	}
}

func TestRemoveAllAsTargetUser_SelfHostedMode(t *testing.T) {
	_ = os.Unsetenv("AGENT_EXEC_USER")

	tempDir := t.TempDir()
	targetDir := filepath.Join(tempDir, "subdir", "nested")

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Create a file inside
	if err := os.WriteFile(filepath.Join(targetDir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Remove the top-level subdir
	if err := removeAllAsTargetUser(filepath.Join(tempDir, "subdir")); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify it's gone
	if _, err := os.Stat(filepath.Join(tempDir, "subdir")); !os.IsNotExist(err) {
		t.Error("Expected directory to be removed")
	}
}

func TestRemoveAllAsTargetUser_NonexistentPath(t *testing.T) {
	_ = os.Unsetenv("AGENT_EXEC_USER")

	tempDir := t.TempDir()
	nonexistent := filepath.Join(tempDir, "does-not-exist")

	// Should not error on non-existent path (same as os.RemoveAll behavior)
	if err := removeAllAsTargetUser(nonexistent); err != nil {
		t.Fatalf("Expected no error for non-existent path, got: %v", err)
	}
}

func TestRemoveAllAsTargetUser_PathNotInAgentHome(t *testing.T) {
	tempDir := t.TempDir()
	targetDir := filepath.Join(tempDir, "toremove")

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	_ = os.Setenv("AGENT_EXEC_USER", "agentrunner")
	defer func() { _ = os.Unsetenv("AGENT_EXEC_USER") }()

	// Path is NOT in /home/agentrunner, so should use direct os.RemoveAll
	if err := removeAllAsTargetUser(targetDir); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Error("Expected directory to be removed")
	}
}

func TestWriteFileAsTargetUser_SelfHostedMode(t *testing.T) {
	_ = os.Unsetenv("AGENT_EXEC_USER")

	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "output.json")

	content := []byte(`{"written": true}`)
	if err := writeFileAsTargetUser(filePath, content, 0644); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	readBack, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read written file: %v", err)
	}

	if string(readBack) != string(content) {
		t.Errorf("Expected %q, got %q", string(content), string(readBack))
	}
}

func TestWriteFileAsTargetUser_PathNotInAgentHome(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "output.json")

	_ = os.Setenv("AGENT_EXEC_USER", "agentrunner")
	defer func() { _ = os.Unsetenv("AGENT_EXEC_USER") }()

	content := []byte(`{"written": true}`)
	if err := writeFileAsTargetUser(filePath, content, 0644); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	readBack, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read written file: %v", err)
	}

	if string(readBack) != string(content) {
		t.Errorf("Expected %q, got %q", string(content), string(readBack))
	}
}

func TestMkdirAllAsTargetUser_SelfHostedMode(t *testing.T) {
	_ = os.Unsetenv("AGENT_EXEC_USER")

	tempDir := t.TempDir()
	dirPath := filepath.Join(tempDir, "a", "b", "c")

	if err := mkdirAllAsTargetUser(dirPath); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	info, err := os.Stat(dirPath)
	if err != nil {
		t.Fatalf("Expected directory to exist, got: %v", err)
	}
	if !info.IsDir() {
		t.Error("Expected a directory")
	}
}

func TestMkdirAllAsTargetUser_PathNotInAgentHome(t *testing.T) {
	tempDir := t.TempDir()
	dirPath := filepath.Join(tempDir, "x", "y")

	_ = os.Setenv("AGENT_EXEC_USER", "agentrunner")
	defer func() { _ = os.Unsetenv("AGENT_EXEC_USER") }()

	if err := mkdirAllAsTargetUser(dirPath); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	info, err := os.Stat(dirPath)
	if err != nil {
		t.Fatalf("Expected directory to exist, got: %v", err)
	}
	if !info.IsDir() {
		t.Error("Expected a directory")
	}
}

// A config is renamed over, not rewritten in place, so a reader holding the old file never sees it
// half written; it keeps its mode.
func TestReplaceFileAsTargetUser_RenamesOverTheFile(t *testing.T) {
	t.Setenv("AGENT_EXEC_USER", "")
	path, old := openOldConfig(t)

	if err := replaceFileAsTargetUser(path, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}

	assertReplaced(t, path, old, 0600)
}

// The script sudo runs as the agent user does the same, with the mode it is given.
func TestReplaceFileScript_RenamesOverTheFile(t *testing.T) {
	path, old := openOldConfig(t)
	cmd := exec.Command("sh", "-c", replaceFileScript, "sh", path, "640")
	cmd.Stdin = strings.NewReader("new")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}

	assertReplaced(t, path, old, 0640)
}

// A new config gets perm; a symlinked one is replaced where the link points, and stays a link.
func TestReplaceFileAsTargetUser_NewAndSymlinkedFiles(t *testing.T) {
	t.Setenv("AGENT_EXEC_USER", "")
	dir := t.TempDir()
	target := filepath.Join(dir, "config.toml")
	link := filepath.Join(dir, "link.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := replaceFileAsTargetUser(target, []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := replaceFileAsTargetUser(link, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}

	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected %s to stay a symlink, got %v, %v", link, info, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("new file mode = %v, %v; want 0644", info, err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "v2" {
		t.Errorf("link target = %q, %v; want v2", got, err)
	}
}

// A link the agent made in its home is replaced as the agent wherever it points, so the agent's
// rights refuse a target outside its home, like nairid's own .env.
func TestReplaceFileAsTargetUser_LinkOutOfTheAgentHome(t *testing.T) {
	homes := t.TempDir()
	t.Setenv("AGENT_EXEC_USER", "agentrunner")
	outside := filepath.Join(homes, "ccagent", ".env")
	link := filepath.Join(homes, "agentrunner", ".claude.json")
	for _, dir := range []string{filepath.Dir(outside), filepath.Dir(link)} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(outside, []byte("NAIRI_KEY=test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	var ranAs []string
	stubAgentUser(t, homes, func(user string, _ ...string) *exec.Cmd {
		ranAs = append(ranAs, user)
		return exec.Command("false") // the agent can't write there
	})

	err := replaceFileAsTargetUser(link, []byte(`{"mcpServers":{}}`), 0600)

	if err == nil || !slices.Equal(ranAs, []string{"agentrunner"}) {
		t.Errorf("error = %v, ran as %v; want a refused write as agentrunner", err, ranAs)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "NAIRI_KEY=test" {
		t.Errorf("the file outside the agent's home = %q, %v; want it untouched", got, err)
	}
}

func stubAgentUser(t *testing.T, homes string, run func(user string, args ...string) *exec.Cmd) {
	t.Helper()
	oldHomes, oldRun := agentHomesDir, runAsUser
	agentHomesDir, runAsUser = homes, run
	t.Cleanup(func() { agentHomesDir, runAsUser = oldHomes, oldRun })
}

// openOldConfig writes a 0600 config and opens it, as a reader would hold it during the write.
func openOldConfig(t *testing.T) (string, *os.File) {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Close() })
	return path, old
}

func assertReplaced(t *testing.T, path string, old *os.File, wantMode os.FileMode) {
	t.Helper()
	if got, err := os.ReadFile(path); err != nil || string(got) != "new" {
		t.Errorf("file = %q, %v; want new", got, err)
	}
	if got, err := io.ReadAll(old); err != nil || string(got) != "old" {
		t.Errorf("the reader holding the old file got %q, %v; want old", got, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != wantMode {
		t.Errorf("mode = %v, %v; want %v", info.Mode().Perm(), err, wantMode)
	}
	if entries, err := os.ReadDir(filepath.Dir(path)); err != nil || len(entries) != 1 {
		t.Errorf("expected no temp file left, got %v, %v", entries, err)
	}
}
