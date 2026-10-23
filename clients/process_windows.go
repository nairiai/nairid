//go:build windows

package clients

import (
	"os/exec"
	"strconv"
)

// Killing only the started process would leave the CLI it launched (node, codex) running,
// so cancelling a run ends the whole process tree.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
	cmd.WaitDelay = WaitDelayAfterCancel
}
