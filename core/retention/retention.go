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

const EnvVar = "NAIRI_LOG_RETENTION_DAYS"

// Days is how many days logs and attachments are kept, from NAIRI_LOG_RETENTION_DAYS. Unset, or not
// a whole number of at least 1, the cleanup is off and nothing is removed, as before it existed.
func Days() (days int, on bool) {
	raw := os.Getenv(EnvVar)
	if raw == "" {
		return 0, false
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 1 {
		log.Warn("⚠️ %s=%q is not a whole number of days of at least 1, so the cleanup stays off", EnvVar, raw)
		return 0, false
	}
	return days, true
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
	entries, err := readOwnDir(dir)
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

// PruneSharedDirs is PruneDirs for a folder that sits in a directory others can write to, such as
// /tmp: it also refuses dir unless the folder it sits in belongs to this process's user and no one
// else may write to it, so no one else, such as the agent user who made the folder first, can swap
// dir for a link between the check and the removal.
func PruneSharedDirs(dir string, cutoff time.Time, remove func(path string) error) (int, error) {
	parent := filepath.Dir(dir)
	info, err := os.Lstat(parent)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return 0, fmt.Errorf("refusing to prune %s: %s is a symbolic link", dir, parent)
	}
	if err := onlyWeCanChange(info); err != nil {
		return 0, fmt.Errorf("refusing to prune %s: %s %w", dir, parent, err)
	}
	return PruneDirs(dir, cutoff, remove)
}

// readOwnDir lists dir, refusing it when dir or its parent is a symbolic link, whose owner would
// choose what gets removed.
func readOwnDir(dir string) ([]os.DirEntry, error) {
	for _, path := range []string{dir, filepath.Dir(dir)} {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing to prune %s: %s is a symbolic link", dir, path)
		}
	}
	return os.ReadDir(dir)
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
