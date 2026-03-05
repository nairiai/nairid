package utils

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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
