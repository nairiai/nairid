package clients

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFilterEnvForAgent(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"NAIRI_API_KEY=secret_api_key",
		"ANTHROPIC_API_KEY=sk-ant-xxx",
		"NAIRI_WS_API_URL=wss://api.example.com",
		"AGENT_EXEC_USER=agentrunner",
		"HOME=/home/user",
	}

	filtered := FilterEnvForAgent(env)

	// Check blocked vars are removed
	for _, e := range filtered {
		for blocked := range BlockedEnvVars {
			if strings.HasPrefix(e, blocked+"=") {
				t.Errorf("Blocked var %s should be filtered out, but found: %s", blocked, e)
			}
		}
	}

	// Check allowed vars are preserved
	expectedVars := map[string]bool{
		"PATH":              false,
		"ANTHROPIC_API_KEY": false,
		"HOME":              false,
	}

	for _, e := range filtered {
		for expected := range expectedVars {
			if strings.HasPrefix(e, expected+"=") {
				expectedVars[expected] = true
			}
		}
	}

	for varName, found := range expectedVars {
		if !found {
			t.Errorf("Expected var %s should be preserved but was not found", varName)
		}
	}

	// Verify count: 6 original - 3 blocked = 3 remaining
	if len(filtered) != 3 {
		t.Errorf("Expected 3 filtered vars, got %d", len(filtered))
	}
}

func TestFilterEnvForAgent_LegacyEksecVars(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"EKSEC_API_KEY=secret_api_key",
		"EKSEC_WS_API_URL=wss://api.example.com",
		"HOME=/home/user",
	}

	filtered := FilterEnvForAgent(env)

	// Legacy EKSEC_* vars should also be blocked
	for _, e := range filtered {
		if strings.HasPrefix(e, "EKSEC_API_KEY=") || strings.HasPrefix(e, "EKSEC_WS_API_URL=") {
			t.Errorf("Legacy EKSEC var should be filtered out, but found: %s", e)
		}
	}

	// Verify count: 4 original - 2 blocked = 2 remaining
	if len(filtered) != 2 {
		t.Errorf("Expected 2 filtered vars, got %d", len(filtered))
	}
}

func TestFilterEnvForAgent_EmptyEnv(t *testing.T) {
	filtered := FilterEnvForAgent([]string{})
	if len(filtered) != 0 {
		t.Errorf("Expected empty filtered env, got %d items", len(filtered))
	}
}

func TestFilterEnvForAgent_NoBlockedVars(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/home/user",
	}

	filtered := FilterEnvForAgent(env)
	if len(filtered) != 2 {
		t.Errorf("Expected 2 filtered vars, got %d", len(filtered))
	}
}

func TestBuildShellCommand(t *testing.T) {
	tests := []struct {
		name     string
		cmdName  string
		args     []string
		expected string
	}{
		{
			name:     "simple command",
			cmdName:  "claude",
			args:     []string{"--version"},
			expected: "claude '--version'",
		},
		{
			name:     "multiple args",
			cmdName:  "claude",
			args:     []string{"--model", "claude-3", "-p", "hello"},
			expected: "claude '--model' 'claude-3' '-p' 'hello'",
		},
		{
			name:     "args with single quotes",
			cmdName:  "claude",
			args:     []string{"-p", "Hello 'world'"},
			expected: "claude '-p' 'Hello '\\''world'\\'''",
		},
		{
			name:     "empty args",
			cmdName:  "claude",
			args:     []string{},
			expected: "claude",
		},
		{
			name:     "args with spaces",
			cmdName:  "claude",
			args:     []string{"-p", "hello world"},
			expected: "claude '-p' 'hello world'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildShellCommand(tt.cmdName, tt.args)
			if result != tt.expected {
				t.Errorf("buildShellCommand(%q, %v) = %q, want %q",
					tt.cmdName, tt.args, result, tt.expected)
			}
		})
	}
}

func TestAgentExecUser(t *testing.T) {
	// Save original value
	original := os.Getenv("AGENT_EXEC_USER")
	defer func() { _ = os.Setenv("AGENT_EXEC_USER", original) }()

	// Test when not set
	_ = os.Unsetenv("AGENT_EXEC_USER")
	if user := AgentExecUser(); user != "" {
		t.Errorf("AgentExecUser() = %q, want empty string", user)
	}

	// Test when set
	_ = os.Setenv("AGENT_EXEC_USER", "agentrunner")
	if user := AgentExecUser(); user != "agentrunner" {
		t.Errorf("AgentExecUser() = %q, want %q", user, "agentrunner")
	}
}

func TestBuildAgentCommandWithContext_SelfHosted(t *testing.T) {
	// Save original value
	original := os.Getenv("AGENT_EXEC_USER")
	defer func() { _ = os.Setenv("AGENT_EXEC_USER", original) }()

	_ = os.Unsetenv("AGENT_EXEC_USER")
	cmd := BuildAgentCommandWithContext(context.Background(), "echo", "hello")

	// In self-hosted mode, should run the command directly
	if cmd.Args[0] != "echo" {
		t.Errorf("Expected echo command in self-hosted mode, got %v", cmd.Args)
	}

	// Check that blocked env vars are filtered
	for _, e := range cmd.Env {
		for blocked := range BlockedEnvVars {
			if strings.HasPrefix(e, blocked+"=") {
				t.Errorf("Blocked var %s should be filtered", blocked)
			}
		}
	}
}

func TestBuildAgentCommandWithContext_Managed(t *testing.T) {
	// Save original value
	original := os.Getenv("AGENT_EXEC_USER")
	defer func() { _ = os.Setenv("AGENT_EXEC_USER", original) }()

	_ = os.Setenv("AGENT_EXEC_USER", "agentrunner")
	cmd := BuildAgentCommandWithContext(context.Background(), "echo", "hello")

	// In managed mode, should use sudo
	if cmd.Args[0] != "sudo" {
		t.Errorf("Expected sudo command in managed mode, got %v", cmd.Args)
	}

	// Verify sudo arguments structure: sudo -u agentrunner bash -c '...'
	// That's 6 args: sudo, -u, agentrunner, bash, -c, <script>
	if len(cmd.Args) != 6 {
		t.Fatalf("Expected 6 args (sudo -u agentrunner bash -c <script>), got %d: %v", len(cmd.Args), cmd.Args)
	}

	expectedPrefix := []string{"sudo", "-u", "agentrunner", "bash", "-c"}
	for i, expected := range expectedPrefix {
		if cmd.Args[i] != expected {
			t.Errorf("Arg %d: expected %q, got %q", i, expected, cmd.Args[i])
		}
	}

	// The bash script (6th arg, index 5) should contain umask 002, env -i, HOME, and the command
	bashScript := cmd.Args[5]

	if !strings.HasPrefix(bashScript, "umask 002 && exec ") {
		t.Errorf("Bash script should start with 'umask 002 && exec ', got: %s", bashScript)
	}

	if !strings.Contains(bashScript, "env -i") {
		t.Error("Bash script should contain 'env -i'")
	}

	if !strings.Contains(bashScript, "HOME=/home/agentrunner") {
		t.Error("Bash script should contain HOME=/home/agentrunner")
	}

	if !strings.Contains(bashScript, "echo 'hello'") {
		t.Errorf("Bash script should contain the command 'echo 'hello'', got: %s", bashScript)
	}

	// cmd.Env should NOT be set in managed mode (env passed via 'env' command inside bash)
	if cmd.Env != nil {
		t.Error("cmd.Env should be nil in managed mode (env passed via 'env' command inside bash)")
	}
}

func TestUpdateHomeForUser(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/home/nairid",
		"USER=nairid",
	}

	result := UpdateHomeForUser(env, "agentrunner")

	// Should have same number of vars
	if len(result) != len(env) {
		t.Errorf("Expected %d vars, got %d", len(env), len(result))
	}

	// HOME should be updated
	hasNewHome := false
	hasOldHome := false
	for _, e := range result {
		if e == "HOME=/home/agentrunner" {
			hasNewHome = true
		}
		if e == "HOME=/home/nairid" {
			hasOldHome = true
		}
	}

	if !hasNewHome {
		t.Error("HOME should be set to /home/agentrunner")
	}
	if hasOldHome {
		t.Error("Old HOME value should be replaced")
	}
}

func TestUpdateHomeForUser_NoHomeInEnv(t *testing.T) {
	// Test case where HOME is not in the environment at all
	env := []string{
		"PATH=/usr/bin",
		"USER=nairid",
	}

	result := UpdateHomeForUser(env, "agentrunner")

	// Should have one more var (HOME added)
	if len(result) != len(env)+1 {
		t.Errorf("Expected %d vars (HOME added), got %d", len(env)+1, len(result))
	}

	// HOME should be set
	hasNewHome := false
	for _, e := range result {
		if e == "HOME=/home/agentrunner" {
			hasNewHome = true
		}
	}

	if !hasNewHome {
		t.Error("HOME should be added as /home/agentrunner when not present in env")
	}
}

func TestAgentHTTPProxy(t *testing.T) {
	// Save original value
	original := os.Getenv("AGENT_HTTP_PROXY")
	defer func() { _ = os.Setenv("AGENT_HTTP_PROXY", original) }()

	// Test when not set
	_ = os.Unsetenv("AGENT_HTTP_PROXY")
	if proxy := AgentHTTPProxy(); proxy != "" {
		t.Errorf("AgentHTTPProxy() = %q, want empty string", proxy)
	}

	// Test when set
	_ = os.Setenv("AGENT_HTTP_PROXY", "http://proxy:8080")
	if proxy := AgentHTTPProxy(); proxy != "http://proxy:8080" {
		t.Errorf("AgentHTTPProxy() = %q, want %q", proxy, "http://proxy:8080")
	}
}

func TestInjectProxyEnv_NoProxy(t *testing.T) {
	// Save original value
	original := os.Getenv("AGENT_HTTP_PROXY")
	defer func() { _ = os.Setenv("AGENT_HTTP_PROXY", original) }()

	_ = os.Unsetenv("AGENT_HTTP_PROXY")

	env := []string{"PATH=/usr/bin", "HOME=/home/user"}
	result := InjectProxyEnv(env)

	// Should return unchanged when no proxy configured
	if len(result) != len(env) {
		t.Errorf("Expected %d vars, got %d", len(env), len(result))
	}
}

func TestInjectProxyEnv_WithProxy(t *testing.T) {
	// Save original value
	original := os.Getenv("AGENT_HTTP_PROXY")
	defer func() { _ = os.Setenv("AGENT_HTTP_PROXY", original) }()

	_ = os.Setenv("AGENT_HTTP_PROXY", "http://proxy:8080")

	env := []string{"PATH=/usr/bin", "HOME=/home/user"}
	result := InjectProxyEnv(env)

	// Should add HTTP_PROXY, http_proxy, HTTPS_PROXY, https_proxy
	expectedLen := len(env) + 4
	if len(result) != expectedLen {
		t.Errorf("Expected %d vars, got %d", expectedLen, len(result))
	}

	// Check that proxy vars are present
	hasHTTPProxy := false
	hasHTTPSProxy := false
	hasLowerHTTPProxy := false
	hasLowerHTTPSProxy := false

	for _, e := range result {
		switch {
		case strings.HasPrefix(e, "HTTP_PROXY=http://proxy:8080"):
			hasHTTPProxy = true
		case strings.HasPrefix(e, "HTTPS_PROXY=http://proxy:8080"):
			hasHTTPSProxy = true
		case strings.HasPrefix(e, "http_proxy=http://proxy:8080"):
			hasLowerHTTPProxy = true
		case strings.HasPrefix(e, "https_proxy=http://proxy:8080"):
			hasLowerHTTPSProxy = true
		}
	}

	if !hasHTTPProxy {
		t.Error("HTTP_PROXY not found in result")
	}
	if !hasHTTPSProxy {
		t.Error("HTTPS_PROXY not found in result")
	}
	if !hasLowerHTTPProxy {
		t.Error("http_proxy not found in result")
	}
	if !hasLowerHTTPSProxy {
		t.Error("https_proxy not found in result")
	}
}

func TestInjectProxyEnv_DoesNotOverride(t *testing.T) {
	// Save original value
	original := os.Getenv("AGENT_HTTP_PROXY")
	defer func() { _ = os.Setenv("AGENT_HTTP_PROXY", original) }()

	_ = os.Setenv("AGENT_HTTP_PROXY", "http://proxy:8080")

	// Env already has proxy vars
	env := []string{
		"PATH=/usr/bin",
		"HTTP_PROXY=http://existing:3128",
		"HTTPS_PROXY=http://existing:3128",
	}
	result := InjectProxyEnv(env)

	// Should not add new proxy vars if they already exist
	if len(result) != len(env) {
		t.Errorf("Expected %d vars (no additions), got %d", len(env), len(result))
	}

	// Verify existing proxy values are preserved
	for _, e := range result {
		if strings.HasPrefix(e, "HTTP_PROXY=") && e != "HTTP_PROXY=http://existing:3128" {
			t.Errorf("HTTP_PROXY was overridden: %s", e)
		}
		if strings.HasPrefix(e, "HTTPS_PROXY=") && e != "HTTPS_PROXY=http://existing:3128" {
			t.Errorf("HTTPS_PROXY was overridden: %s", e)
		}
	}
}

func TestBuildAgentCommandWithContextAndWorkDir(t *testing.T) {
	// Save original value
	original := os.Getenv("AGENT_EXEC_USER")
	defer func() { _ = os.Setenv("AGENT_EXEC_USER", original) }()

	_ = os.Unsetenv("AGENT_EXEC_USER")

	workDir := "/tmp/test-workdir"
	cmd := BuildAgentCommandWithContextAndWorkDir(context.Background(), workDir, "echo", "hello")

	// Should set working directory
	if cmd.Dir != workDir {
		t.Errorf("Expected cmd.Dir to be %q, got %q", workDir, cmd.Dir)
	}

	// Should still have the correct command
	if cmd.Args[0] != "echo" {
		t.Errorf("Expected command 'echo', got %v", cmd.Args)
	}
}

func TestBuildAgentCommandWithContextAndWorkDir_EmptyWorkDir(t *testing.T) {
	// Save original value
	original := os.Getenv("AGENT_EXEC_USER")
	defer func() { _ = os.Setenv("AGENT_EXEC_USER", original) }()

	_ = os.Unsetenv("AGENT_EXEC_USER")

	// Empty workDir should not set Dir
	cmd := BuildAgentCommandWithContextAndWorkDir(context.Background(), "", "echo", "hello")

	if cmd.Dir != "" {
		t.Errorf("Expected cmd.Dir to be empty when workDir is empty, got %q", cmd.Dir)
	}
}

func TestBuildAgentCommandWithContextAndWorkDir_Managed(t *testing.T) {
	// Save original value
	original := os.Getenv("AGENT_EXEC_USER")
	defer func() { _ = os.Setenv("AGENT_EXEC_USER", original) }()

	_ = os.Setenv("AGENT_EXEC_USER", "agentrunner")

	workDir := "/tmp/test-workdir"
	cmd := BuildAgentCommandWithContextAndWorkDir(context.Background(), workDir, "echo", "hello")

	// In managed mode, should use sudo
	if cmd.Args[0] != "sudo" {
		t.Errorf("Expected sudo command in managed mode, got %v", cmd.Args)
	}

	// Should still set working directory
	if cmd.Dir != workDir {
		t.Errorf("Expected cmd.Dir to be %q in managed mode, got %q", workDir, cmd.Dir)
	}
}

func TestInjectProxyEnv_WithMCPProxy(t *testing.T) {
	// Save original values
	origHTTPProxy := os.Getenv("AGENT_HTTP_PROXY")
	origMCPProxy := os.Getenv("AGENT_MCP_PROXY")
	defer func() {
		_ = os.Setenv("AGENT_HTTP_PROXY", origHTTPProxy)
		_ = os.Setenv("AGENT_MCP_PROXY", origMCPProxy)
	}()

	_ = os.Setenv("AGENT_HTTP_PROXY", "http://proxy:8080")
	_ = os.Setenv("AGENT_MCP_PROXY", "http://mcp-proxy.internal:8082")

	env := []string{"PATH=/usr/bin", "HOME=/home/user"}
	result := InjectProxyEnv(env)

	// Should add HTTP_PROXY, http_proxy, HTTPS_PROXY, https_proxy, NO_PROXY, no_proxy
	expectedLen := len(env) + 6
	if len(result) != expectedLen {
		t.Errorf("Expected %d vars, got %d: %v", expectedLen, len(result), result)
	}

	hasNoProxy := false
	hasLowerNoProxy := false
	for _, e := range result {
		if e == "NO_PROXY=mcp-proxy.internal" {
			hasNoProxy = true
		}
		if e == "no_proxy=mcp-proxy.internal" {
			hasLowerNoProxy = true
		}
	}

	if !hasNoProxy {
		t.Error("NO_PROXY=mcp-proxy.internal not found in result")
	}
	if !hasLowerNoProxy {
		t.Error("no_proxy=mcp-proxy.internal not found in result")
	}
}

func TestInjectProxyEnv_NoMCPProxy(t *testing.T) {
	// Save original values
	origHTTPProxy := os.Getenv("AGENT_HTTP_PROXY")
	origMCPProxy := os.Getenv("AGENT_MCP_PROXY")
	defer func() {
		_ = os.Setenv("AGENT_HTTP_PROXY", origHTTPProxy)
		_ = os.Setenv("AGENT_MCP_PROXY", origMCPProxy)
	}()

	_ = os.Setenv("AGENT_HTTP_PROXY", "http://proxy:8080")
	_ = os.Unsetenv("AGENT_MCP_PROXY")

	env := []string{"PATH=/usr/bin", "HOME=/home/user"}
	result := InjectProxyEnv(env)

	// Should NOT add NO_PROXY when AGENT_MCP_PROXY is not set
	for _, e := range result {
		if strings.HasPrefix(e, "NO_PROXY=") || strings.HasPrefix(e, "no_proxy=") {
			t.Errorf("NO_PROXY should not be set when AGENT_MCP_PROXY is not configured, found: %s", e)
		}
	}
}

func TestExtractHost(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"http://mcp-proxy.internal:8082", "mcp-proxy.internal"},
		{"http://127.0.0.1:8082", "127.0.0.1"},
		{"http://localhost:8082/path", "localhost"},
		{"", ""},
		{"not-a-url", ""},
	}

	for _, tt := range tests {
		result := extractHost(tt.input)
		if result != tt.expected {
			t.Errorf("extractHost(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestBuildAgentCommandWithContext_InjectsProxy(t *testing.T) {
	// Save original values
	origUser := os.Getenv("AGENT_EXEC_USER")
	origProxy := os.Getenv("AGENT_HTTP_PROXY")
	defer func() {
		_ = os.Setenv("AGENT_EXEC_USER", origUser)
		_ = os.Setenv("AGENT_HTTP_PROXY", origProxy)
	}()

	_ = os.Unsetenv("AGENT_EXEC_USER")
	_ = os.Setenv("AGENT_HTTP_PROXY", "http://secret-proxy:8080")

	cmd := BuildAgentCommandWithContext(context.Background(), "echo", "hello")

	// Check that proxy vars are in the command's environment
	hasHTTPProxy := false
	hasHTTPSProxy := false

	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "HTTP_PROXY=http://secret-proxy:8080") {
			hasHTTPProxy = true
		}
		if strings.HasPrefix(e, "HTTPS_PROXY=http://secret-proxy:8080") {
			hasHTTPSProxy = true
		}
	}

	if !hasHTTPProxy {
		t.Error("HTTP_PROXY not injected into command environment")
	}
	if !hasHTTPSProxy {
		t.Error("HTTPS_PROXY not injected into command environment")
	}
}

func TestConfigureProcessGroup_SelfHosted(t *testing.T) {
	original := os.Getenv("AGENT_EXEC_USER")
	defer func() { _ = os.Setenv("AGENT_EXEC_USER", original) }()

	_ = os.Unsetenv("AGENT_EXEC_USER")
	cmd := BuildAgentCommandWithContext(context.Background(), "echo", "hello")

	// Should have SysProcAttr with Setpgid
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr should be set")
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Error("Setpgid should be true")
	}

	// Should have Cancel function
	if cmd.Cancel == nil {
		t.Error("Cancel function should be set")
	}

	// Should have WaitDelay
	if cmd.WaitDelay != WaitDelayAfterKill {
		t.Errorf("WaitDelay = %v, want %v", cmd.WaitDelay, WaitDelayAfterKill)
	}
}

func TestConfigureProcessGroup_Managed(t *testing.T) {
	original := os.Getenv("AGENT_EXEC_USER")
	defer func() { _ = os.Setenv("AGENT_EXEC_USER", original) }()

	_ = os.Setenv("AGENT_EXEC_USER", "agentrunner")
	cmd := BuildAgentCommandWithContext(context.Background(), "echo", "hello")

	// Should have SysProcAttr with Setpgid
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr should be set")
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Error("Setpgid should be true")
	}

	// Should have Cancel function
	if cmd.Cancel == nil {
		t.Error("Cancel function should be set")
	}

	// Should have WaitDelay
	if cmd.WaitDelay != WaitDelayAfterKill {
		t.Errorf("WaitDelay = %v, want %v", cmd.WaitDelay, WaitDelayAfterKill)
	}
}

func TestProcessGroupKill_KillsChildProcesses(t *testing.T) {
	original := os.Getenv("AGENT_EXEC_USER")
	defer func() { _ = os.Setenv("AGENT_EXEC_USER", original) }()
	_ = os.Unsetenv("AGENT_EXEC_USER")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	cmd := BuildAgentCommandWithContext(ctx, "sh", "-c", "sleep 300 & wait")

	err := cmd.Start()
	if err != nil {
		t.Fatalf("failed to start command: %v", err)
	}

	pgid := cmd.Process.Pid

	// Wait for the process to be killed by context timeout
	_ = cmd.Wait()

	// Wait a short time for the OS to fully reap the process group
	time.Sleep(100 * time.Millisecond)

	// Verify the process group is dead by trying to signal it.
	err = syscall.Kill(-pgid, 0)
	if err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		t.Error("process group should be dead after context cancellation, but it's still alive")
	}
}
