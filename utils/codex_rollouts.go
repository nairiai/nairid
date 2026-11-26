package utils

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var codexRolloutDirs = []string{"sessions", "archived_sessions"}

// PruneCodexRollouts removes the Codex session transcripts under codexHome last modified before
// cutoff, except those of liveSessionIDs, whose jobs may still be resumed. It also removes the
// prompt history Codex kept before history was switched off, once that file is older than cutoff.
func PruneCodexRollouts(codexHome string, cutoff time.Time, liveSessionIDs []string) (int, error) {
	removed := 0
	var errs []error
	for _, dir := range codexRolloutDirs {
		err := filepath.WalkDir(filepath.Join(codexHome, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if entry.IsDir() || !isCodexRollout(entry.Name()) || hasAnySessionID(entry.Name(), liveSessionIDs) {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.ModTime().Before(cutoff) {
				return nil
			}
			if err := removeAllAsTargetUser(path); err != nil {
				errs = append(errs, err)
				return nil
			}
			removed++
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	history := filepath.Join(codexHome, "history.jsonl")
	if info, err := os.Lstat(history); err == nil && info.Mode().IsRegular() && info.ModTime().Before(cutoff) {
		if err := removeAllAsTargetUser(history); err != nil {
			errs = append(errs, err)
		} else {
			removed++
		}
	}
	return removed, errors.Join(errs...)
}

func isCodexRollout(name string) bool {
	return strings.HasPrefix(name, "rollout-") && (strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.zst"))
}

func hasAnySessionID(name string, sessionIDs []string) bool {
	for _, id := range sessionIDs {
		if id != "" && strings.Contains(name, id) {
			return true
		}
	}
	return false
}
