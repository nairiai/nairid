// Package clients provides utilities for spawning agent processes.
package clients

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultSessionTimeout is the maximum duration an agent CLI session can run
// before being killed. This prevents hung processes from blocking the worker pool.
const DefaultSessionTimeout = 1 * time.Hour

// BlockedEnvVars lists environment variables that should never be passed to agent processes.
// These contain sensitive credentials that agents should not have access to.
var BlockedEnvVars = map[string]bool{
	"EKSEC_API_KEY":    true,
	"EKSEC_WS_API_URL": true,
	"CCAGENT_API_KEY":    true, // Legacy env var
	"CCAGENT_WS_API_URL": true, // Legacy env var
	"AGENT_EXEC_USER":    true,
	"AGENT_HTTP_PROXY":   true, // This is for eksecd to read, not for agents
	"AGENT_MCP_PROXY":    true, // This is for eksecd to read, not for agents
}

// AgentExecUser returns the configured user for running agent processes.
// Returns empty string if not configured (self-hosted mode).
func AgentExecUser() string {
	return os.Getenv("AGENT_EXEC_USER")
}

// AgentMCPProxy returns the MCP proxy URL for proxying MCP server connections.
// When set, eksecd configures agents to use HTTP URLs pointing to the MCP proxy
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
// When the context expires, the process is killed automatically.
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
// This prevents agent processes from accessing credentials like EKSEC_API_KEY.
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
// eksecd process itself does not use the proxy (allowing it to reach the backend).
func InjectProxyEnv(env []string) []string {
	proxyURL := AgentHTTPProxy()
	if proxyURL == "" {
		return env
	}

	// Check if HTTP_PROXY or HTTPS_PROXY already exist in env
	hasHTTPProxy := false
	hasHTTPSProxy := false
	for _, e := range env {
		if strings.HasPrefix(e, "HTTP_PROXY=") || strings.HasPrefix(e, "http_proxy=") {
			hasHTTPProxy = true
		}
		if strings.HasPrefix(e, "HTTPS_PROXY=") || strings.HasPrefix(e, "https_proxy=") {
			hasHTTPSProxy = true
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

	return env
}
