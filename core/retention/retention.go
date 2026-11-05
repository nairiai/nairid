// Package retention bounds how long plain-text copies of conversation data stay on the host.
package retention

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nairid/core/log"
)

const (
	EnvVar      = "NAIRI_LOG_RETENTION_DAYS"
	DefaultDays = 30
)

// Days is how many days logs and attachments are kept: NAIRI_LOG_RETENTION_DAYS, or 30 when
// it is unset or not a whole number of at least 1.
func Days() int {
	raw := os.Getenv(EnvVar)
	if raw == "" {
		return DefaultDays
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 1 {
		log.Warn("⚠️ %s=%q is not a whole number of days of at least 1, keeping %d days", EnvVar, raw, DefaultDays)
		return DefaultDays
	}
	return days
}

// PruneFiles removes the files directly in dir whose name starts with one of prefixes (any, when
// empty) and ends with one of suffixes (any, when empty) and which were last modified before
// cutoff. A file for which skip returns true is kept. A missing dir prunes nothing.
func PruneFiles(dir string, prefixes, suffixes []string, cutoff time.Time, skip func(name string) bool) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", dir, err)
	}
	removed := 0
	var errs []error
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !hasAnyPrefix(name, prefixes) || !hasAnySuffix(name, suffixes) {
			continue
		}
		if skip != nil && skip(name) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// PruneDirs removes, with remove, each directory directly in dir whose files were all last
// modified before cutoff. An empty directory goes by its own modification time. A missing dir
// prunes nothing.
func PruneDirs(dir string, cutoff time.Time, remove func(path string) error) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", dir, err)
	}
	removed := 0
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		newest, err := newestModTime(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !newest.Before(cutoff) {
			continue
		}
		if err := remove(path); err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

func newestModTime(dir string) (time.Time, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return time.Time{}, err
	}
	newest := info.ModTime()
	err = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, err
}

func hasAnyPrefix(name string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func hasAnySuffix(name string, suffixes []string) bool {
	if len(suffixes) == 0 {
		return true
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
