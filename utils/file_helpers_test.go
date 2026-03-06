package utils

import (
	"os"
	"path/filepath"
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
