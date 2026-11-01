//go:build windows

package clients

import (
	"os/exec"
	"strconv"
	"time"
)

// Killing only the started process would leave the CLI it launched (node, codex) running,
// so cancelling a run ends the whole process tree.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = WaitDelayAfterCancel
}

// A hung fetch has no state to save, so cancel kills its whole tree immediately. WaitDelay
// bounds how long a descendant can hold the output pipes open before Go closes them itself.
func configureFetchProcessGroup(cmd *exec.Cmd, waitDelay time.Duration) {
	cmd.Cancel = func() error {
		if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = waitDelay
}
