package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"nairid/core/log"
	"nairid/core/retention"
	"nairid/services"
	"nairid/utils"
)

type cleanupRecorder struct {
	services.CLIAgent
	mu   sync.Mutex
	days []int
}

func (c *cleanupRecorder) CleanupOldLogs(maxAgeDays int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.days = append(c.days, maxAgeDays)
	return nil
}

func (c *cleanupRecorder) recorded() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int(nil), c.days...)
}

// agedAttachmentDirs points the config dir and the inbound attachments dir at temp folders and puts
// a 60-day-old attachment folder in each, so nothing on the host running the test is touched.
func agedAttachmentDirs(t *testing.T) (outbound, inbound string) {
	t.Helper()
	configDir := t.TempDir()
	t.Setenv("NAIRI_CONFIG_DIR", configDir)

	previousInbound := utils.InboundAttachmentsDir
	utils.InboundAttachmentsDir = filepath.Join(t.TempDir(), "eksecd", "attachments")
	t.Cleanup(func() { utils.InboundAttachmentsDir = previousInbound })

	old := time.Now().AddDate(0, 0, -60)
	outbound = filepath.Join(configDir, "attachments", "job-old")
	inbound = filepath.Join(utils.InboundAttachmentsDir, "job-old")
	for _, dir := range []string{outbound, inbound} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{filepath.Join(dir, "file.txt"), dir} {
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Chmod(filepath.Dir(utils.InboundAttachmentsDir), 0700); err != nil {
		t.Fatal(err)
	}
	return outbound, inbound
}

// agedSessionLogs puts a 60-day-old session log of two CLIs in the config dir's logs folder.
func agedSessionLogs(t *testing.T) []string {
	t.Helper()
	logsDir := filepath.Join(os.Getenv("NAIRI_CONFIG_DIR"), "logs")
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -60)
	var paths []string
	for _, name := range []string{"claude-session-old.log", "codex-session-old.log"} {
		path := filepath.Join(logsDir, name)
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

func allGone(paths ...string) bool {
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			return false
		}
	}
	return true
}

func TestRetentionRoutine_OffKeepsEverythingButOldSessionLogs(t *testing.T) {
	t.Setenv(retention.EnvVar, "")
	outbound, inbound := agedAttachmentDirs(t)
	agent := &cleanupRecorder{}
	cr := &CmdRunner{cliAgent: agent}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cr.startRetentionRoutine(ctx)

	if got := agent.recorded(); !reflect.DeepEqual(got, []int{7}) {
		t.Errorf("session log cleanup calls = %v, want only the 7-day one an agent always had", got)
	}
	for _, dir := range []string{outbound, inbound} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s was removed while %s is unset: %v", dir, retention.EnvVar, err)
		}
	}
}

func TestRetentionRoutine_OnStartsTheCleanupWithTheSetDays(t *testing.T) {
	t.Setenv(retention.EnvVar, "30")
	outbound, inbound := agedAttachmentDirs(t)
	writer, err := log.NewRotatingWriter(log.RotatingWriterConfig{
		LogDir:     filepath.Join(os.Getenv("NAIRI_CONFIG_DIR"), "logs"),
		FilePrefix: "eksecd",
		MaxAge:     30 * 24 * time.Hour,
		MaxBytes:   1 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	sessionLogs := agedSessionLogs(t)
	agent := &cleanupRecorder{}
	cr := &CmdRunner{cliAgent: agent, rotatingWriter: writer}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cr.startRetentionRoutine(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !allGone(append(sessionLogs, outbound, inbound)...) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := agent.recorded(); len(got) != 0 {
		t.Errorf("the current CLI's own 7-day cleanup ran %v; with the switch on the routine prunes every CLI's logs instead", got)
	}
	for _, path := range sessionLogs {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s should be pruned by the routine once %s=30: %v", filepath.Base(path), retention.EnvVar, err)
		}
	}
	for _, dir := range []string{outbound, inbound} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s should be pruned by the routine once %s=30: %v", dir, retention.EnvVar, err)
		}
	}
}

func TestRetentionRoutine_OnPrunesWithTheSetDays(t *testing.T) {
	t.Setenv(retention.EnvVar, "30")
	outbound, inbound := agedAttachmentDirs(t)
	configDir := os.Getenv("NAIRI_CONFIG_DIR")
	writer, err := log.NewRotatingWriter(log.RotatingWriterConfig{
		LogDir:     filepath.Join(configDir, "logs"),
		FilePrefix: "eksecd",
		MaxAge:     30 * 24 * time.Hour,
		MaxBytes:   1 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	sessionLogs := agedSessionLogs(t)
	cr := &CmdRunner{cliAgent: &cleanupRecorder{}, rotatingWriter: writer}

	cr.pruneOldData(30)

	if !allGone(sessionLogs...) {
		t.Error("a 60-day-old session log of some CLI was kept with the switch at 30 days")
	}
	for _, dir := range []string{outbound, inbound} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s should be pruned once %s=30: %v", dir, retention.EnvVar, err)
		}
	}
}

func TestProgramLogConfig_KeepsAboutAGigabyteAndTheSetDays(t *testing.T) {
	config := programLogConfig(t.TempDir(), 30)
	if config.MaxBytes != 1<<30 || config.MaxAge != 30*24*time.Hour || config.MaxFileSize != 10*1024*1024 {
		t.Errorf("program log config = %+v, want 1 GiB, 30 days and 10 MiB files", config)
	}
}
