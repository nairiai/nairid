package utils

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"nairid/core/log"
)

// readFileAsTargetUser reads content from a file, using sudo if necessary.
// When AGENT_EXEC_USER is set and the target path is in that user's home directory,
// the file is read via 'sudo -u <user> cat' to ensure we can read files owned by
// the agent user even if they have restrictive permissions (e.g., 0600).
func readFileAsTargetUser(filePath string) ([]byte, error) {
	execUser := os.Getenv("AGENT_EXEC_USER")
	if execUser == "" {
		// Self-hosted mode: read directly
		return os.ReadFile(filePath)
	}

	// Check if the target path is in the agent user's home directory
	agentHome := "/home/" + execUser
	if !strings.HasPrefix(filePath, agentHome) {
		// Not in agent's home, read directly
		return os.ReadFile(filePath)
	}

	log.Info("📖 Reading file as user '%s': %s", execUser, filePath)

	cmd := exec.Command("sudo", "-u", execUser, "cat", filePath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Check if file doesn't exist by examining the error
		if strings.Contains(stderr.String(), "No such file or directory") {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("failed to read file as user %s: %w (stderr: %s)", execUser, err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// agentHomesDir and runAsUser are variables so tests can stand in for /home and sudo.
var (
	agentHomesDir = "/home"
	runAsUser     = func(user string, args ...string) *exec.Cmd {
		return exec.Command("sudo", append([]string{"-u", user}, args...)...)
	}
)

// replaceFileAsTargetUser writes content to a temp file next to filePath and renames it over, so
// an agent CLI never reads its config half written. An existing file keeps its mode. The user comes
// from the path before links resolve, so a link in the agent's home can't reach past its rights.
func replaceFileAsTargetUser(filePath string, content []byte, perm os.FileMode) error {
	execUser := os.Getenv("AGENT_EXEC_USER")
	asAgent := execUser != "" && strings.HasPrefix(filepath.Clean(filePath), filepath.Join(agentHomesDir, execUser)+"/")
	if resolved, err := filepath.EvalSymlinks(filePath); err == nil {
		filePath = resolved
	}
	if info, err := os.Stat(filePath); err == nil {
		perm = info.Mode().Perm()
	}
	if !asAgent {
		return replaceFile(filePath, content, perm)
	}

	log.Info("🔌 Replacing file as user '%s': %s", execUser, filePath)
	cmd := runAsUser(execUser, "sh", "-c", replaceFileScript, "sh", filePath, fmt.Sprintf("%o", perm))
	cmd.Stdin = bytes.NewReader(content)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to replace file as user %s: %w (stderr: %s)", execUser, err, stderr.String())
	}
	return nil
}

// replaceFileScript is replaceFile for sudo: $1 is the path, $2 the octal mode, stdin the content.
const replaceFileScript = `tmp=$(mktemp "$1.XXXXXX") || exit 1
if cat > "$tmp" && chmod "$2" "$tmp" && mv -f "$tmp" "$1"; then exit 0; fi
rm -f "$tmp"
exit 1`

func replaceFile(filePath string, content []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(filePath), filepath.Base(filePath)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(content)
	if err == nil {
		err = tmp.Chmod(perm)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filePath)
}

// removeAllAsTargetUser removes a path and all its children, using sudo if necessary.
// When AGENT_EXEC_USER is set and the target path is in that user's home directory,
// the removal is done via 'sudo -u <user> rm -rf' to ensure we can remove files/directories
// owned by the agent user even if they have restrictive permissions.
func removeAllAsTargetUser(path string) error {
	execUser := os.Getenv("AGENT_EXEC_USER")
	if execUser == "" {
		// Self-hosted mode: remove directly
		return os.RemoveAll(path)
	}

	// Check if the target path is in the agent user's home directory
	agentHome := "/home/" + execUser
	if !strings.HasPrefix(path, agentHome) {
		// Not in agent's home, remove directly
		return os.RemoveAll(path)
	}

	log.Info("🗑️ Removing path as user '%s': %s", execUser, path)

	cmd := exec.Command("sudo", "-u", execUser, "rm", "-rf", path)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to remove path as user %s: %w (stderr: %s)", execUser, err, stderr.String())
	}

	return nil
}
