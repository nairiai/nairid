//go:build windows

package clients

import "os/exec"

// configureProcessGroup is a no-op on Windows.
// Process group isolation via Setpgid and SIGKILL are POSIX-only.
// On Windows, cmd.Process.Kill() is used by default which terminates the process.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.WaitDelay = WaitDelayAfterKill
}
