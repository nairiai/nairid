package env

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"nairid/core/log"
)

// SanitizeNamespace converts a raw identifier (agent ID, repo identifier, etc.)
// into a filesystem-safe directory name suitable for use as a namespace subdirectory.
//
// Examples:
//
//	"my-agent"                     -> "my-agent"
//	"github.com/owner/repo"        -> "github.com__owner__repo"
//	"github.com/owner/repo.git"    -> "github.com__owner__repo.git"
//	"agent with spaces"            -> "agent-with-spaces"
//	""                             -> "default"
func SanitizeNamespace(raw string) string {
	if raw == "" {
		return "default"
	}

	// Replace forward and back slashes with double underscore (readable in ls output)
	sanitized := strings.ReplaceAll(raw, "/", "__")
	sanitized = strings.ReplaceAll(sanitized, "\\", "__")

	// Replace colons (Windows paths, URLs) with double underscore
	sanitized = strings.ReplaceAll(sanitized, ":", "__")

	// Replace spaces with hyphens
	sanitized = strings.ReplaceAll(sanitized, " ", "-")

	// Remove any remaining characters that aren't alphanumeric, hyphen, underscore, or dot
	reg := regexp.MustCompile(`[^\w\-.]`)
	sanitized = reg.ReplaceAllString(sanitized, "-")

	// Collapse consecutive hyphens/underscores
	sanitized = regexp.MustCompile(`-{2,}`).ReplaceAllString(sanitized, "-")

	// Remove leading/trailing dots and hyphens to avoid hidden files or ugly names
	sanitized = strings.Trim(sanitized, ".-")

	if sanitized == "" {
		return "default"
	}

	return sanitized
}

// ResolveInstanceNamespace determines the unique namespace for this nairid instance.
// The namespace is used to isolate per-instance paths (worktrees, state, logs) so that
// multiple nairid processes on the same machine under the same UNIX user don't collide.
//
// Resolution order:
//  1. NAIRI_AGENT_ID env var (explicit, highest priority)
//  2. EKSEC_AGENT_ID env var (legacy)
//  3. repoIdentifier (e.g., "github.com/owner/repo") — derived from git remote
//  4. Error if none available (no-repo mode requires NAIRI_AGENT_ID)
//
// The returned value is sanitized for safe use as a directory name.
func ResolveInstanceNamespace(envManager *EnvManager, repoIdentifier string) (string, error) {
	agentID := envManager.Get("NAIRI_AGENT_ID")
	if agentID == "" {
		agentID = envManager.Get("EKSEC_AGENT_ID")
	}

	if agentID != "" {
		return SanitizeNamespace(agentID), nil
	}

	if repoIdentifier != "" {
		return SanitizeNamespace(repoIdentifier), nil
	}

	return "", fmt.Errorf("cannot determine instance namespace: set NAIRI_AGENT_ID environment variable (required when running multiple instances or in no-repo mode)")
}

// NamespacedInstanceDir returns the per-instance directory under configDir and creates it.
// Layout: <configDir>/instances/<namespace>/
func NamespacedInstanceDir(configDir, namespace string) (string, error) {
	dir := filepath.Join(configDir, "instances", namespace)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create namespaced config directory: %w", err)
	}
	return dir, nil
}

// NamespacedStatePath returns the state.json path inside the namespaced instance directory.
func NamespacedStatePath(instanceDir string) string {
	return filepath.Join(instanceDir, "state.json")
}

// NamespacedLogsDir returns the logs directory inside the namespaced instance directory.
func NamespacedLogsDir(instanceDir string) string {
	return filepath.Join(instanceDir, "logs")
}

// MigrateLegacyState copies a legacy (non-namespaced) state.json to the namespaced path
// if the namespaced file does not yet exist. This provides a smooth upgrade path for
// existing single-instance setups.
func MigrateLegacyState(configDir, namespacedStatePath string) {
	if _, err := os.Stat(namespacedStatePath); !os.IsNotExist(err) {
		return // namespaced state already exists (or stat error) — nothing to do
	}

	legacyPath := filepath.Join(configDir, "state.json")
	legacyData, err := os.ReadFile(legacyPath)
	if err != nil {
		return // no legacy file to migrate
	}

	log.Info("📦 Migrating legacy state.json to namespaced path: %s", namespacedStatePath)
	if err := os.WriteFile(namespacedStatePath, legacyData, 0644); err != nil {
		log.Warn("⚠️ Failed to migrate legacy state.json: %v", err)
	}
}
