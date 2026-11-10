package clients

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func stubGitIdentity(t *testing.T, name, email string) {
	t.Helper()
	original := daemonGitIdentity
	daemonGitIdentity = func() (string, string) { return name, email }
	t.Cleanup(func() { daemonGitIdentity = original })
}

// runRepoCommandScript runs the bash script a managed command would hand to sudo, without sudo.
func runRepoCommandScript(t *testing.T, cmd *exec.Cmd) map[string]string {
	t.Helper()
	out, err := exec.Command("bash", "-c", cmd.Args[len(cmd.Args)-1]).Output()
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, value, _ := strings.Cut(line, "=")
		env[key] = value
	}
	return env
}

func TestNewRepoCommand_SelfHosted(t *testing.T) {
	t.Setenv("AGENT_EXEC_USER", "")

	cmd := newRepoCommand("git", "status")
	if !slices.Equal(cmd.Args, []string{"git", "status"}) {
		t.Errorf("Args = %v, want plain git status", cmd.Args)
	}
	if cmd.Env != nil {
		t.Errorf("Env should be nil so the daemon env is inherited, got %d entries", len(cmd.Env))
	}

	withExtra := newRepoCommandContext(context.Background(), []string{"GIT_TERMINAL_PROMPT=0"}, "git", "ls-remote")
	if !slices.Contains(withExtra.Env, "GIT_TERMINAL_PROMPT=0") {
		t.Error("extra env should be appended in self-hosted mode")
	}
}

func TestNewRepoCommand_Managed(t *testing.T) {
	t.Setenv("AGENT_EXEC_USER", "agentrunner")
	t.Setenv("NAIRI_API_KEY", "fake-key")
	t.Setenv("AGENT_HTTP_PROXY", "http://proxy.invalid:3128")
	t.Setenv("HOME", "/home/ccagent")
	t.Setenv("GH_TOKEN", "fake-gh")
	stubGitIdentity(t, "nairi-ai", "agent@nairi.ai")

	cmd := newRepoCommandContext(context.Background(), []string{"GIT_SSH_COMMAND=ssh -o BatchMode=yes"}, "env")
	// Failure messages never print the script: it carries the env.
	if len(cmd.Args) != 7 || !slices.Equal(cmd.Args[:6], []string{"sudo", "-n", "-u", "agentrunner", "bash", "-c"}) {
		t.Fatal("want a sudo -n -u agentrunner bash -c wrapper")
	}
	if !strings.HasPrefix(cmd.Args[6], "umask 002 && exec env -i ") {
		t.Error("script should clear the env")
	}

	env := runRepoCommandScript(t, cmd)
	for _, blocked := range []string{"NAIRI_API_KEY", "AGENT_EXEC_USER", "AGENT_HTTP_PROXY"} {
		if _, ok := env[blocked]; ok {
			t.Errorf("%s must not reach the command", blocked)
		}
	}
	want := map[string]string{
		"HTTP_PROXY":          "http://proxy.invalid:3128",
		"HTTPS_PROXY":         "http://proxy.invalid:3128",
		"http_proxy":          "http://proxy.invalid:3128",
		"https_proxy":         "http://proxy.invalid:3128",
		"NO_PROXY":            "localhost,127.0.0.1,::1",
		"no_proxy":            "localhost,127.0.0.1,::1",
		"HOME":                "/home/agentrunner",
		"GH_TOKEN":            "fake-gh",
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_CONFIG_COUNT":    "3",
		"GIT_CONFIG_KEY_0":    "safe.directory",
		"GIT_CONFIG_VALUE_0":  "*",
		"GIT_CONFIG_KEY_1":    "user.name",
		"GIT_CONFIG_VALUE_1":  "nairi-ai",
		"GIT_CONFIG_KEY_2":    "user.email",
		"GIT_CONFIG_VALUE_2":  "agent@nairi.ai",
		"GIT_SSH_COMMAND":     "ssh -o BatchMode=yes",
	}
	for key, value := range want {
		if env[key] != value {
			t.Errorf("%s = %q, want %q", key, env[key], value)
		}
	}
}

func TestNewRepoCommand_ManagedWithoutProxy(t *testing.T) {
	t.Setenv("AGENT_EXEC_USER", "agentrunner")
	t.Setenv("AGENT_HTTP_PROXY", "")
	proxyKeys := []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"}
	for _, key := range proxyKeys {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	stubGitIdentity(t, "", "")

	env := runRepoCommandScript(t, newRepoCommand("env"))
	for _, key := range proxyKeys {
		if _, ok := env[key]; ok {
			t.Errorf("%s should not be set without AGENT_HTTP_PROXY", key)
		}
	}
}

func TestNewRepoCommand_ManagedKeepsExplicitProxy(t *testing.T) {
	t.Setenv("AGENT_EXEC_USER", "agentrunner")
	t.Setenv("AGENT_HTTP_PROXY", "http://proxy.invalid:3128")
	t.Setenv("HTTPS_PROXY", "http://corp.invalid:8080")
	stubGitIdentity(t, "", "")

	env := runRepoCommandScript(t, newRepoCommand("env"))
	if env["HTTPS_PROXY"] != "http://corp.invalid:8080" {
		t.Errorf("HTTPS_PROXY = %q, want the explicit one kept", env["HTTPS_PROXY"])
	}
}

func TestNewRepoCommand_ManagedQuotesArgs(t *testing.T) {
	t.Setenv("AGENT_EXEC_USER", "agentrunner")
	stubGitIdentity(t, "", "")

	cmd := newRepoCommand("git", "commit", "-m", "it's a $(test) `msg`")
	script := cmd.Args[len(cmd.Args)-1]
	if !strings.HasSuffix(script, ` git 'commit' '-m' 'it'\''s a $(test) `+"`msg`'") {
		t.Error("args should be single-quoted after the env")
	}
}

func TestManagedGitEnv_WithoutIdentity(t *testing.T) {
	stubGitIdentity(t, "", "")

	env := managedGitEnv()
	want := []string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=safe.directory", "GIT_CONFIG_VALUE_0=*"}
	if !slices.Equal(env, want) {
		t.Errorf("managedGitEnv() = %v, want %v", env, want)
	}
}

func TestRebuildCommand(t *testing.T) {
	t.Setenv("AGENT_EXEC_USER", "agentrunner")
	stubGitIdentity(t, "", "")

	cmd := newRepoCommand("git", "push", "origin", "main")
	cmd.Dir = "/repo"
	next := rebuildCommand(cmd)
	if next == cmd {
		t.Fatal("rebuildCommand should return a new command")
	}
	if next.Path != cmd.Path || !slices.Equal(next.Args, cmd.Args) || next.Dir != cmd.Dir || !slices.Equal(next.Env, cmd.Env) {
		t.Error("rebuilt command should keep Path, Args, Dir and Env")
	}
}

func setupAgentHome(t *testing.T) string {
	t.Helper()
	t.Setenv("AGENT_EXEC_USER", "agentrunner")
	original := agentHomesDir
	agentHomesDir = t.TempDir()
	t.Cleanup(func() { agentHomesDir = original })
	home := filepath.Join(agentHomesDir, "agentrunner")
	if err := os.Mkdir(home, 0755); err != nil {
		t.Fatal(err)
	}
	return home
}

func assertGroupWritable(t *testing.T, dir string) {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o070 != 0o070 {
		t.Errorf("%s mode = %o, want group rwx", dir, info.Mode().Perm())
	}
}

func TestEnsureWorktreeBaseDir_CreatesGroupWritable(t *testing.T) {
	home := setupAgentHome(t)

	base := filepath.Join(home, ".eksec_worktrees", "ns")
	if err := EnsureWorktreeBaseDir(base); err != nil {
		t.Fatal(err)
	}
	assertGroupWritable(t, filepath.Join(home, ".eksec_worktrees"))
	assertGroupWritable(t, base)
}

func TestEnsureWorktreeBaseDir_FixesExistingDir(t *testing.T) {
	home := setupAgentHome(t)
	base := filepath.Join(home, ".eksec_worktrees")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}

	if err := EnsureWorktreeBaseDir(base); err != nil {
		t.Fatal(err)
	}
	assertGroupWritable(t, base)
}

func TestEnsureWorktreeBaseDir_KeepsSetgid(t *testing.T) {
	home := setupAgentHome(t)
	base := filepath.Join(home, ".eksec_worktrees")
	if err := os.Mkdir(base, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0755|os.ModeSetgid); err != nil {
		t.Skipf("cannot set setgid here: %v", err)
	}

	if err := EnsureWorktreeBaseDir(base); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(base)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSetgid == 0 {
		t.Error("setgid should be kept")
	}
	assertGroupWritable(t, base)
}

func TestEnsureWorktreeBaseDir_RefusesLinkOutOfHome(t *testing.T) {
	home := setupAgentHome(t)
	outside := t.TempDir()
	if err := os.Chmod(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".eksec_worktrees")); err != nil {
		t.Fatal(err)
	}

	if err := EnsureWorktreeBaseDir(filepath.Join(home, ".eksec_worktrees", "ns")); err == nil {
		t.Fatal("expected an error for a link that leaves the agent home")
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("outside dir mode changed to %o", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(outside, "ns")); err == nil {
		t.Error("nothing should be created outside the agent home")
	}
}

func TestEnsureWorktreeBaseDir_RejectsPathOutsideHome(t *testing.T) {
	home := setupAgentHome(t)

	for _, dir := range []string{home, filepath.Dir(home), filepath.Join(filepath.Dir(home), "ccagent", "x")} {
		if err := EnsureWorktreeBaseDir(dir); err == nil {
			t.Errorf("expected an error for %s", dir)
		}
	}
}

func TestEnsureWorktreeBaseDir_SelfHosted(t *testing.T) {
	t.Setenv("AGENT_EXEC_USER", "")

	base := filepath.Join(t.TempDir(), "a", "b")
	if err := EnsureWorktreeBaseDir(base); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(base); err != nil || !info.IsDir() {
		t.Errorf("base dir not created: %v", err)
	}
}

func TestReadRepoFile_SelfHosted(t *testing.T) {
	t.Setenv("AGENT_EXEC_USER", "")

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".github", "pull_request_template.md"), []byte("## Summary"), 0644); err != nil {
		t.Fatal(err)
	}

	content, err := readRepoFile(dir, ".github/pull_request_template.md")
	if err != nil || string(content) != "## Summary" {
		t.Errorf("readRepoFile = %q, %v", content, err)
	}
	if _, err := readRepoFile(dir, "missing.md"); err == nil {
		t.Error("expected an error for a missing file")
	}
}
