package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gofrs/flock"
)

// DirLock represents a directory-based lock using the current working directory
type DirLock struct {
	lockFile *flock.Flock
	lockPath string
}

// sanitizeDirPath converts a directory path to a safe filename
// Replaces special characters that could cause filesystem issues
func sanitizeDirPath(dirPath string) string {
	// Replace forward and back slashes with --
	sanitized := strings.ReplaceAll(dirPath, "/", "--")
	sanitized = strings.ReplaceAll(sanitized, "\\", "--")

	// Replace other problematic characters with safe alternatives
	sanitized = strings.ReplaceAll(sanitized, ":", "--")
	sanitized = strings.ReplaceAll(sanitized, "*", "-star-")
	sanitized = strings.ReplaceAll(sanitized, "?", "-q-")
	sanitized = strings.ReplaceAll(sanitized, "\"", "-quote-")
	sanitized = strings.ReplaceAll(sanitized, "<", "-lt-")
	sanitized = strings.ReplaceAll(sanitized, ">", "-gt-")
	sanitized = strings.ReplaceAll(sanitized, "|", "-pipe-")

	// Remove any remaining problematic characters using regex
	reg := regexp.MustCompile(`[^\w\-.]`)
	sanitized = reg.ReplaceAllString(sanitized, "-")

	// Remove leading/trailing dots and dashes to avoid hidden files
	sanitized = strings.Trim(sanitized, ".-")

	// Ensure we have a non-empty filename
	if sanitized == "" {
		sanitized = "default"
	}

	return sanitized
}

// NewDirLock creates a new directory lock for the specified path.
// If path is empty, it uses the current working directory.
func NewDirLock(path string) (*DirLock, error) {
	lockDir := path

	// If no path provided, use current working directory
	if lockDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current working directory: %w", err)
		}
		lockDir = cwd
	}

	// Sanitize the directory path to create a safe filename
	sanitizedDir := sanitizeDirPath(lockDir)

	// Get system temp directory
	tempDir := os.TempDir()

	// Create eksecd subdirectory in temp
	eksecdTempDir := filepath.Join(tempDir, "eksecd")
	if err := os.MkdirAll(eksecdTempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create eksecd temp directory: %w", err)
	}

	// Create lock file path using sanitized directory name
	lockFileName := fmt.Sprintf("%s.lock", sanitizedDir)
	lockPath := filepath.Join(eksecdTempDir, lockFileName)

	// Create flock instance
	lockFile := flock.New(lockPath)

	return &DirLock{
		lockFile: lockFile,
		lockPath: lockPath,
	}, nil
}

// TryLock attempts to acquire the directory lock
// Returns nil if successful, error if lock is already held or other error occurs
func (dl *DirLock) TryLock() error {
	locked, err := dl.lockFile.TryLock()
	if err != nil {
		return fmt.Errorf("failed to try lock: %w", err)
	}

	if !locked {
		return fmt.Errorf("another eksecd instance is already running in this path")
	}

	return nil
}

// Unlock releases the directory lock and removes the lock file
func (dl *DirLock) Unlock() error {
	if dl.lockFile == nil {
		return nil
	}

	// Unlock the file
	err := dl.lockFile.Unlock()
	if err != nil {
		return fmt.Errorf("failed to unlock: %w", err)
	}

	// Remove the lock file
	if err := os.Remove(dl.lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove lock file: %w", err)
	}

	return nil
}

// GetLockPath returns the path to the lock file (for debugging/testing)
func (dl *DirLock) GetLockPath() string {
	return dl.lockPath
}
