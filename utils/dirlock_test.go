package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeDirPath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/home/user/project", "home--user--project"},
		{"C:\\Users\\user\\project", "C----Users--user--project"},
		{"/path/with:special*chars?", "path--with--special-star-chars-q"},
		{"/path/with<>|\"quotes", "path--with-lt--gt--pipe--quote-quotes"},
		{"", "default"},
		{"...", "default"},
		{"---", "default"},
		{"/normal/path", "normal--path"},
	}

	for _, test := range tests {
		result := sanitizeDirPath(test.input)
		if result != test.expected {
			t.Errorf("sanitizeDirPath(%q) = %q, expected %q", test.input, result, test.expected)
		}
	}
}

func TestNewDirLock(t *testing.T) {
	lock, err := NewDirLock("")
	if err != nil {
		t.Fatalf("NewDirLock() failed: %v", err)
	}

	// Verify lock path contains expected components
	lockPath := lock.GetLockPath()
	if !strings.Contains(lockPath, "eksecd") {
		t.Errorf("Lock path should contain 'nairid': %s", lockPath)
	}

	if !strings.HasSuffix(lockPath, ".lock") {
		t.Errorf("Lock path should end with '.lock': %s", lockPath)
	}

	// Verify the eksecd directory was created
	eksecdDir := filepath.Dir(lockPath)
	if _, err := os.Stat(eksecdDir); os.IsNotExist(err) {
		t.Errorf("eksecd directory should be created: %s", eksecdDir)
	}
}

func TestDirLockTryLockAndUnlock(t *testing.T) {
	lock1, err := NewDirLock("")
	if err != nil {
		t.Fatalf("NewDirLock() failed: %v", err)
	}

	// First lock should succeed
	err = lock1.TryLock()
	if err != nil {
		t.Fatalf("First TryLock() should succeed: %v", err)
	}

	// Second lock from same directory should fail
	lock2, err := NewDirLock("")
	if err != nil {
		t.Fatalf("Second NewDirLock() failed: %v", err)
	}

	err = lock2.TryLock()
	if err == nil {
		t.Errorf("Second TryLock() should fail when directory is already locked")
		// Clean up the unexpected lock
		if unlockErr := lock2.Unlock(); unlockErr != nil {
			t.Errorf("Failed to unlock lock2: %v", unlockErr)
		}
	}

	// Unlock the first lock
	err = lock1.Unlock()
	if err != nil {
		t.Errorf("Unlock() failed: %v", err)
	}

	// Verify lock file was removed
	if _, err := os.Stat(lock1.GetLockPath()); !os.IsNotExist(err) {
		t.Errorf("Lock file should be removed after unlock: %s", lock1.GetLockPath())
	}

	// Third lock should now succeed after first was unlocked
	lock3, err := NewDirLock("")
	if err != nil {
		t.Fatalf("Third NewDirLock() failed: %v", err)
	}

	err = lock3.TryLock()
	if err != nil {
		t.Errorf("Third TryLock() should succeed after first was unlocked: %v", err)
	}

	// Clean up
	if err := lock3.Unlock(); err != nil {
		t.Errorf("Failed to unlock lock3: %v", err)
	}
}

func TestDirLockUnlockIdempotent(t *testing.T) {
	lock, err := NewDirLock("")
	if err != nil {
		t.Fatalf("NewDirLock() failed: %v", err)
	}

	// Lock first
	err = lock.TryLock()
	if err != nil {
		t.Fatalf("TryLock() failed: %v", err)
	}

	// Unlock should succeed
	err = lock.Unlock()
	if err != nil {
		t.Errorf("First Unlock() failed: %v", err)
	}

	// Second unlock should not fail
	err = lock.Unlock()
	if err != nil {
		t.Errorf("Second Unlock() should not fail: %v", err)
	}
}

func TestNewDirLockWithExplicitPath(t *testing.T) {
	// Create a temp directory to use as the lock path
	tempDir, err := os.MkdirTemp("", "dirlock-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	lock, err := NewDirLock(tempDir)
	if err != nil {
		t.Fatalf("NewDirLock(tempDir) failed: %v", err)
	}

	// Verify lock path is based on the provided path, not cwd
	lockPath := lock.GetLockPath()
	sanitizedTempDir := sanitizeDirPath(tempDir)
	if !strings.Contains(lockPath, sanitizedTempDir) {
		t.Errorf("Lock path should contain sanitized temp dir path: got %s, expected to contain %s", lockPath, sanitizedTempDir)
	}

	// Verify locking works
	err = lock.TryLock()
	if err != nil {
		t.Fatalf("TryLock() should succeed: %v", err)
	}

	// Clean up
	if err := lock.Unlock(); err != nil {
		t.Errorf("Failed to unlock: %v", err)
	}
}

func TestDirLockDifferentPathsAreIndependent(t *testing.T) {
	// Create two temp directories
	tempDir1, err := os.MkdirTemp("", "dirlock-test1-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory 1: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir1) }()

	tempDir2, err := os.MkdirTemp("", "dirlock-test2-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory 2: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir2) }()

	// Create locks for both paths
	lock1, err := NewDirLock(tempDir1)
	if err != nil {
		t.Fatalf("NewDirLock(tempDir1) failed: %v", err)
	}

	lock2, err := NewDirLock(tempDir2)
	if err != nil {
		t.Fatalf("NewDirLock(tempDir2) failed: %v", err)
	}

	// Both locks should succeed since they're for different paths
	err = lock1.TryLock()
	if err != nil {
		t.Fatalf("First lock TryLock() should succeed: %v", err)
	}

	err = lock2.TryLock()
	if err != nil {
		t.Errorf("Second lock TryLock() should succeed for different path: %v", err)
	}

	// Clean up
	if err := lock1.Unlock(); err != nil {
		t.Errorf("Failed to unlock lock1: %v", err)
	}
	if err := lock2.Unlock(); err != nil {
		t.Errorf("Failed to unlock lock2: %v", err)
	}
}
