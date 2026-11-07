package clients

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"nairid/core/log"
)

// agentHomesDir is a var so tests can stand in for /home.
var agentHomesDir = "/home"

// newRepoCommand builds a command that runs in the repo or a job worktree (git, gh, cat).
// The agent can write there, hooks and config included, so in managed mode the command runs
// as the agent user with the agent's env, never as nairid's user.
func newRepoCommand(name string, args ...string) *exec.Cmd {
	return newRepoCommandContext(context.Background(), nil, name, args...)
}

func newRepoCommandContext(ctx context.Context, extraEnv []string, name string, args ...string) *exec.Cmd {
	execUser := AgentExecUser()
	if execUser == "" {
		cmd := exec.CommandContext(ctx, name, args...)
		if len(extraEnv) > 0 {
			cmd.Env = append(os.Environ(), extraEnv...)
		}
		return cmd
	}

	env := UpdateHomeForUser(FilterEnvForAgent(os.Environ()), execUser)
	env = append(env, managedGitEnv()...)
	env = append(env, extraEnv...)
	quoted := make([]string, 0, len(env))
	for _, entry := range env {
		quoted = append(quoted, shellQuote(entry))
	}
	script := "umask 002 && exec env -i " + strings.Join(quoted, " ") + " " + buildShellCommand(name, args)
	return exec.CommandContext(ctx, "sudo", "-n", "-u", execUser, "bash", "-c", script)
}

// managedGitEnv passes config at command scope: git reads safe.directory only from protected
// config, and the repo is still owned by nairid's user. Commits keep nairid's identity. sudo can
// give git a fresh tty, so prompts are off or a bad token would hang the turn.
func managedGitEnv() []string {
	pairs := [][2]string{{"safe.directory", "*"}}
	name, email := daemonGitIdentity()
	if name != "" {
		pairs = append(pairs, [2]string{"user.name", name})
	}
	if email != "" {
		pairs = append(pairs, [2]string{"user.email", email})
	}
	env := []string{"GIT_TERMINAL_PROMPT=0", fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(pairs))}
	for i, pair := range pairs {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, pair[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, pair[1]))
	}
	return env
}

// daemonGitIdentity reads nairid's own global identity. It runs from / so no repo config is
// involved, and `config --get` runs no hooks or helpers. A var so tests can stub it.
var daemonGitIdentity = sync.OnceValues(func() (string, string) {
	get := func(key string) string {
		cmd := exec.Command("git", "config", "--global", "--get", key)
		cmd.Dir = "/"
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return get("user.name"), get("user.email")
})

// rebuildCommand copies a finished command for a retry. Args carries the sudo wrapper.
func rebuildCommand(cmd *exec.Cmd) *exec.Cmd {
	next := exec.Command(cmd.Args[0], cmd.Args[1:]...)
	next.Dir = cmd.Dir
	next.Env = cmd.Env
	return next
}

// readRepoFile reads a file from an agent-writable tree as the agent user, so a link the agent
// planted can't make nairid read one of its own files. An empty dir means the working directory.
func readRepoFile(dir, path string) ([]byte, error) {
	if AgentExecUser() == "" {
		return os.ReadFile(filepath.Join(dir, path))
	}
	cmd := newRepoCommand("cat", "--", path)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// EnsureWorktreeBaseDir creates the directory job worktrees go in. In managed mode the agent user
// adds worktrees there, so it must be group-writable, and older versions created it 0755. os.Root
// keeps a link the agent planted from pointing the chmod outside the agent's home.
func EnsureWorktreeBaseDir(dir string) error {
	execUser := AgentExecUser()
	if execUser == "" {
		return os.MkdirAll(dir, 0755)
	}
	home := filepath.Join(agentHomesDir, execUser)
	rel, err := filepath.Rel(home, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("worktree base %s is not inside %s", dir, home)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(rel, 0775); err != nil {
		return err
	}
	for p := rel; p != "."; p = filepath.Dir(p) {
		info, err := root.Stat(p)
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o070 == 0o070 {
			continue
		}
		// Keeps setgid so new worktrees stay in the daemon's group.
		mode := info.Mode()&(fs.ModePerm|fs.ModeSetgid) | 0o070
		// Fails only for a dir nairid doesn't own; git reports it if the agent can't write there.
		if err := root.Chmod(p, mode); err != nil {
			log.Warn("⚠️ Could not make %s group-writable: %v", filepath.Join(home, p), err)
		}
	}
	return nil
}
