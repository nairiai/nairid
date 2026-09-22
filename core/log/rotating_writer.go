package log

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RotatingWriter provides size-based log rotation with thread safety
type RotatingWriter struct {
	logDir      string
	maxFileSize int64
	filePrefix  string

	mu          sync.Mutex
	currentFile *os.File
	currentPath string
	currentSize int64
	stdout      io.Writer
}

// RotatingWriterConfig holds configuration for the rotating writer
type RotatingWriterConfig struct {
	LogDir      string    // Directory where log files will be created
	MaxFileSize int64     // Maximum size per file in bytes (default: 10MB)
	FilePrefix  string    // Prefix for log file names (default: "app")
	Stdout      io.Writer // Writer for stdout output (default: os.Stdout)
}

// NewRotatingWriter creates a new rotating writer with the specified configuration
func NewRotatingWriter(config RotatingWriterConfig) (*RotatingWriter, error) {
	// Set defaults
	if config.MaxFileSize <= 0 {
		config.MaxFileSize = 10 * 1024 * 1024 // 10MB
	}
	if config.FilePrefix == "" {
		config.FilePrefix = "app"
	}
	if config.Stdout == nil {
		config.Stdout = os.Stdout
	}

	// Create log directory if it doesn't exist
	if err := os.MkdirAll(config.LogDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	rw := &RotatingWriter{
		logDir:      config.LogDir,
		maxFileSize: config.MaxFileSize,
		filePrefix:  config.FilePrefix,
		stdout:      config.Stdout,
	}

	// Create initial log file
	if err := rw.rotateFile(); err != nil {
		return nil, fmt.Errorf("failed to create initial log file: %w", err)
	}

	return rw, nil
}

// Write implements io.Writer interface with automatic rotation
func (rw *RotatingWriter) Write(p []byte) (n int, err error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	// Always write to stdout first
	if _, err := rw.stdout.Write(p); err != nil {
		// Continue even if stdout write fails
		fmt.Fprintf(os.Stderr, "Warning: Failed to write to stdout: %v\n", err)
	}

	// Check if we need to rotate the log file
	if rw.currentSize+int64(len(p)) > rw.maxFileSize {
		if err := rw.rotateFile(); err != nil {
			// If rotation fails, continue with current file
			fmt.Fprintf(os.Stderr, "Warning: Failed to rotate log file: %v\n", err)
		}
	}

	// Write to current log file
	if rw.currentFile != nil {
		n, err = rw.currentFile.Write(p)
		rw.currentSize += int64(n)
		return n, err
	}

	return len(p), nil
}

// rotateFile creates a new log file and closes the current one
func (rw *RotatingWriter) rotateFile() error {
	// Close current file if it exists
	if rw.currentFile != nil {
		if err := rw.currentFile.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to close current log file: %v\n", err)
		}
	}

	// Create new log file with timestamp
	timestamp := time.Now().Format("20060102-150405")
	logFileName := fmt.Sprintf("%s-%s.log", rw.filePrefix, timestamp)
	newLogFilePath := filepath.Join(rw.logDir, logFileName)

	newLogFile, err := os.OpenFile(newLogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("failed to create new log file: %w", err)
	}

	// Update current file references
	rw.currentFile = newLogFile
	rw.currentPath = newLogFilePath
	rw.currentSize = 0

	return nil
}

// Close closes the current log file
func (rw *RotatingWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if rw.currentFile != nil {
		err := rw.currentFile.Close()
		rw.currentFile = nil
		return err
	}
	return nil
}

// GetCurrentLogPath returns the path of the current log file
func (rw *RotatingWriter) GetCurrentLogPath() string {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.currentPath
}

// GetCurrentFileSize returns the current size of the active log file
func (rw *RotatingWriter) GetCurrentFileSize() int64 {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.currentSize
}
