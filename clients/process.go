// Package clients provides utilities for spawning agent processes.
package clients

import (
	"context"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// WaitDelayAfterKill is the duration to wait for I/O to drain after killing
// a process. This prevents cmd.Wait() from blocking forever when orphaned
// child processes keep the stdout pipe open.
const WaitDelayAfterKill = 10 * time.Second

// DefaultSessionTimeout is the fallback maximum duration an agent CLI session
// can run before being killed. Prefer SessionTimeout() at runtime, which
// honours the NAIRI_MAX_SESSION_MS env var override.
const DefaultSessionTimeout = 1 * time.Hour

// BlockedEnvVars lists environment variables that should never be passed to agent processes.
// These contain sensitive credentials or nairid-internal config that agents should not see.
var BlockedEnvVars = map[string]bool{
	"NAIRI_API_KEY":        true,
	"NAIRI_WS_API_URL":     true,
	"NAIRI_MAX_SESSION_MS": true, // nairid-only config (CLI session timeout)
	"EKSEC_API_KEY":        true, // Legacy env var
	"EKSEC_WS_API_URL":     true, // Legacy env var
	"EKSEC_MAX_SESSION_MS": true, // Legacy env var
	"CCAGENT_API_KEY":      true, // Legacy env var
	"CCAGENT_WS_API_URL":   true, // Legacy env var
	"AGENT_EXEC_USER":      true,
	"AGENT_HTTP_PROXY":     true, // This is for nairid to read, not for agents
	"AGENT_MCP_PROXY":      true, // This is for nairid to read, not for agents
}

// SessionTimeout returns the per-CLI-session timeout used to bound a single
// agent invocation (claude, codex, opencode, cursor).
//
// Resolution order:
//  1. NAIRI_MAX_SESSION_MS — integer number of milliseconds (must be > 0)
//  2. EKSEC_MAX_SESSION_MS — legacy fallback, same format
//  3. DefaultSessionTimeout (1 hour) when neither is set or the value is invalid
//
// Operators who need long-running single-turn sessions can bump this without
// recompiling nairid. The value is read on every call so a deployment can pick
// up changes by restarting the daemon.
func SessionTimeout() time.Duration {
	raw := os.Getenv("NAIRI_MAX_SESSION_MS")
	source := "NAIRI_MAX_SESSION_MS"
	if raw == "" {
		raw = os.Getenv("EKSEC_MAX_SESSION_MS")
		source = "EKSEC_MAX_SESSION_MS"
	}
	if raw == "" {
		return DefaultSessionTimeout
	}

	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms <= 0 {
		log.Printf("[SessionTimeout] invalid %s=%q, falling back to default %s", source, raw, DefaultSessionTimeout)
		return DefaultSessionTimeout
	}

	return time.Duration(ms) * time.Millisecond
}

// AgentExecUser returns the configured user for running agent processes.
// Returns empty string if not configured (self-hosted mode).
func AgentExecUser() string {
	return os.Getenv("AGENT_EXEC_USER")
}

// AgentMCPProxy returns the MCP proxy URL for proxying MCP server connections.
// When set, nairid configures agents to use HTTP URLs pointing to the MCP proxy
// instead of spawning local stdio MCP server processes.
// Returns empty string if not configured.
func AgentMCPProxy() string {
	return os.Getenv("AGENT_MCP_PROXY")
}

// AgentHTTPProxy returns the HTTP proxy URL that agent processes should use.
// This is read from AGENT_HTTP_PROXY and injected into agent processes as HTTP_PROXY/HTTPS_PROXY.
// Returns empty string if not configured.
func AgentHTTPProxy() string {
	return os.Getenv("AGENT_HTTP_PROXY")
}

// BuildAgentCommandWithContext creates an exec.Cmd bound to a context for timeout/cancellation.
// When the context expires, the entire process tree is killed via process group signal.
func BuildAgentCommandWithContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	execUser := AgentExecUser()
	filteredEnv := FilterEnvForAgent(os.Environ())

	// Inject HTTP proxy settings for agent processes if configured
	filteredEnv = InjectProxyEnv(filteredEnv)

	log.Printf("[BuildAgentCommandWithContext] execUser=%q, name=%q", execUser, name)

	if execUser == "" {
		// Self-hosted mode: run as current user
		log.Printf("[BuildAgentCommandWithContext] Self-hosted mode: running %s as current user", name)
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = filteredEnv
		configureProcessGroup(cmd)
		return cmd
	}

	// Managed mode: run as specified user via sudo
	filteredEnv = UpdateHomeForUser(filteredEnv, execUser)

	shellCmd := buildShellCommand(name, args)
	envArgs := make([]string, 0, len(filteredEnv)+1)
	envArgs = append(envArgs, "env", "-i")
	envArgs = append(envArgs, filteredEnv...)
	envCmd := strings.Join(envArgs, " ") + " " + shellCmd

	bashScript := "umask 002 && exec " + envCmd
	sudoArgs := []string{"-u", execUser, "bash", "-c", bashScript}

	log.Printf("[BuildAgentCommandWithContext] Managed mode: running sudo -u %s bash -c '...' (cmd=%s)", execUser, name)
	cmd := exec.CommandContext(ctx, "sudo", sudoArgs...)
	configureProcessGroup(cmd)
	return cmd
}

// BuildAgentCommandWithContextAndWorkDir creates a context-bound exec.Cmd in the specified working directory.
func BuildAgentCommandWithContextAndWorkDir(ctx context.Context, workDir, name string, args ...string) *exec.Cmd {
	cmd := BuildAgentCommandWithContext(ctx, name, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

// buildShellCommand safely constructs a shell command string with escaped arguments.
// Single quotes are escaped using the '\" pattern.
func buildShellCommand(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	for _, arg := range args {
		// Escape single quotes in arguments
		escaped := strings.ReplaceAll(arg, "'", "'\\''")
		parts = append(parts, "'"+escaped+"'")
	}
	return strings.Join(parts, " ")
}

// FilterEnvForAgent removes sensitive variables from environment.
// This prevents agent processes from accessing credentials like NAIRI_API_KEY.
func FilterEnvForAgent(env []string) []string {
	var filtered []string
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) < 1 {
			continue
		}
		key := parts[0]
		if !BlockedEnvVars[key] {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// UpdateHomeForUser updates the HOME environment variable to point to the specified user's home directory.
// This is necessary when running as a different user to ensure the process can write to its own home
// and find config files like .claude.json for MCP server configurations.
func UpdateHomeForUser(env []string, username string) []string {
	newHome := "/home/" + username
	result := make([]string, 0, len(env)+1)
	foundHome := false

	for _, e := range env {
		if strings.HasPrefix(e, "HOME=") {
			// Replace existing HOME
			result = append(result, "HOME="+newHome)
			foundHome = true
		} else {
			result = append(result, e)
		}
	}

	// Always ensure HOME is set, even if it wasn't in the original environment
	if !foundHome {
		result = append(result, "HOME="+newHome)
	}

	return result
}

// InjectProxyEnv adds HTTP_PROXY and HTTPS_PROXY to the environment if AGENT_HTTP_PROXY is set.
// This ensures agent processes route their traffic through the secret proxy while the
// nairid process itself does not use the proxy (allowing it to reach the backend).
// When AGENT_MCP_PROXY is also set, it adds the MCP proxy hostname to NO_PROXY so that
// agent processes can reach the MCP proxy directly without going through the secret proxy.
func InjectProxyEnv(env []string) []string {
	proxyURL := AgentHTTPProxy()
	if proxyURL == "" {
		return env
	}

	// Check if HTTP_PROXY or HTTPS_PROXY already exist in env
	hasHTTPProxy := false
	hasHTTPSProxy := false
	hasNoProxy := false
	for _, e := range env {
		if strings.HasPrefix(e, "HTTP_PROXY=") || strings.HasPrefix(e, "http_proxy=") {
			hasHTTPProxy = true
		}
		if strings.HasPrefix(e, "HTTPS_PROXY=") || strings.HasPrefix(e, "https_proxy=") {
			hasHTTPSProxy = true
		}
		if strings.HasPrefix(e, "NO_PROXY=") || strings.HasPrefix(e, "no_proxy=") {
			hasNoProxy = true
		}
	}

	// Only add if not already present (don't override explicit settings)
	if !hasHTTPProxy {
		env = append(env, "HTTP_PROXY="+proxyURL)
		env = append(env, "http_proxy="+proxyURL) // Some tools use lowercase
	}
	if !hasHTTPSProxy {
		env = append(env, "HTTPS_PROXY="+proxyURL)
		env = append(env, "https_proxy="+proxyURL) // Some tools use lowercase
	}

	// Always bypass the proxy for loopback addresses. Some agent CLIs (notably
	// OpenCode v2) run a local background server and connect to it over loopback;
	// if that self-connection is routed through the secret proxy the CLI can never
	// reach its own server and hangs ("Timed out waiting for the background service
	// to start"). Excluding loopback is inert for agents that do not use a local
	// server (OpenCode v1, Claude, Codex), so this is safe for both OpenCode v1 and
	// v2 without any version detection.
	//
	// When an MCP proxy is configured its hostname is added too, so agent processes
	// bypass the HTTP proxy for MCP connections: the secret proxy runs in a separate
	// container and cannot resolve the MCP proxy's internal hostname
	// (mcp-proxy.internal), causing 502 errors if MCP traffic is routed through it.
	if !hasNoProxy {
		noProxyHosts := []string{"localhost", "127.0.0.1", "::1"}
		if mcpProxyURL := AgentMCPProxy(); mcpProxyURL != "" {
			if mcpHost := extractHost(mcpProxyURL); mcpHost != "" {
				noProxyHosts = append(noProxyHosts, mcpHost)
			}
		}
		noProxy := strings.Join(noProxyHosts, ",")
		env = append(env, "NO_PROXY="+noProxy)
		env = append(env, "no_proxy="+noProxy)
	}

	return env
}

// configureProcessGroup is defined in process_unix.go and process_windows.go

// extractHost extracts the hostname (without port) from a URL string.
func extractHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
