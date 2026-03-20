//go:build !windows

package clients

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup sets up process group isolation and kill behavior for agent commands.
// It ensures that when the context expires (timeout), the entire process tree is killed —
// not just the top-level process. Without this, child processes (e.g. opencode, node) survive
// as orphans and hold stdout pipes open, causing cmd.Wait() to block indefinitely.
//
// It also sets WaitDelay so that if the pipe is still held open after the process is killed
// (e.g. by a grandchild process that wasn't in the process group), Wait() returns after
// a bounded delay instead of blocking forever.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Kill the entire process group (negative PID) instead of just the leader.
		// This ensures sudo → bash → node → opencode all get SIGKILL.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = WaitDelayAfterKill
}
