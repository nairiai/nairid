package services

import (
	"fmt"
	"time"

	"nairid/core/log"
	"nairid/core/retention"
)

// sessionLogPrefixes covers every CLI, so switching the agent's CLI strands no old logs.
var sessionLogPrefixes = []string{"claude-session-", "codex-session-", "cursor-session-", "opencode-session-"}

func PruneSessionLogs(logDir string, maxAgeDays int) error {
	if maxAgeDays <= 0 {
		return fmt.Errorf("maxAgeDays must be greater than 0")
	}
	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	removed, err := retention.PruneFiles(logDir, sessionLogPrefixes, []string{".log"}, cutoff, nil)
	if err != nil {
		return fmt.Errorf("failed to prune session logs: %w", err)
	}
	log.Info("📋 Removed %d session log(s) older than %d days", removed, maxAgeDays)
	return nil
}
