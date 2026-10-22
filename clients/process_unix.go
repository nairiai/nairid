//go:build !windows

package clients

import (
	"os/exec"
	"syscall"
	"time"
)

// A cancelled run gets the same treatment as a Ctrl-C in a terminal: SIGINT lets
// the CLI end its turn and save its session, and only a CLI that ignores that is
// terminated, then killed. Variables so tests can shorten the waits.
var (
	stopGraceAfterInterrupt = 10 * time.Second
	stopGraceAfterTerm      = 20 * time.Second
)

// The command gets its own process group so signals reach every child (sudo → bash → node → cli).
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return stopProcessGroup(cmd.Process.Pid)
	}
	cmd.WaitDelay = WaitDelayAfterCancel
}

func stopProcessGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGINT); err != nil {
		return err
	}
	go func() {
		time.Sleep(stopGraceAfterInterrupt)
		if !groupAlive(pid) {
			return
		}
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		time.Sleep(stopGraceAfterTerm)
		if !groupAlive(pid) {
			return
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}()
	return nil
}

func groupAlive(pid int) bool {
	return syscall.Kill(-pid, 0) == nil
}
