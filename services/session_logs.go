package services

import (
	"fmt"
	"time"

	"nairid/core/log"
	"nairid/core/retention"
)

// AllSessionLogPrefixes covers every CLI, so the retention routine strands no logs of a CLI the
// agent used before. Each CLI's own startup cleanup passes only its prefix, as it always has.
var AllSessionLogPrefixes = []string{"claude-session-", "codex-session-", "cursor-session-", "opencode-session-"}

func PruneSessionLogs(logDir string, prefixes []string, maxAgeDays int) error {
	if maxAgeDays <= 0 {
		return fmt.Errorf("maxAgeDays must be greater than 0")
	}
	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	removed, err := retention.PruneFiles(logDir, prefixes, []string{".log"}, cutoff, nil)
	if err != nil {
		return fmt.Errorf("failed to prune session logs: %w", err)
	}
	log.Info("📋 Removed %d session log(s) older than %d days", removed, maxAgeDays)
	return nil
}
