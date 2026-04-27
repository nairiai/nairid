package env

import (
	"fmt"
	"regexp"
	"strings"
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
	// 1. Check for explicit agent ID env vars
	agentID := envManager.Get("NAIRI_AGENT_ID")
	if agentID == "" {
		agentID = envManager.Get("EKSEC_AGENT_ID")
	}

	if agentID != "" {
		return SanitizeNamespace(agentID), nil
	}

	// 2. Fall back to repository identifier
	if repoIdentifier != "" {
		return SanitizeNamespace(repoIdentifier), nil
	}

	// 3. No identifier available — this means no-repo mode without NAIRI_AGENT_ID
	return "", fmt.Errorf("cannot determine instance namespace: set NAIRI_AGENT_ID environment variable (required when running multiple instances or in no-repo mode)")
}
