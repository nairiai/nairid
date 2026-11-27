package testsupport

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"nairid/core/log"
)

// InstallFakeCLI puts an executable called name first on PATH that exits 0 without output.
func InstallFakeCLI(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type LogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// CaptureLog routes every log line to the returned buffer until the test ends.
func CaptureLog(t *testing.T) *LogBuffer {
	t.Helper()
	logs := &LogBuffer{}
	log.SetWriterWithLevel(logs, slog.LevelDebug)
	t.Cleanup(func() { log.SetWriterWithLevel(os.Stdout, slog.LevelError+1) })
	return logs
}
