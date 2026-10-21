//go:build !windows

package clients

import (
	"os/exec"
	"strconv"
	"strings"
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

// configureProcessGroup puts the command in its own process group so signals
// reach every child (sudo → bash → node → cli), and bounds Wait with WaitDelay so
// a grandchild that holds the stdout pipe cannot block it forever.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return stopProcessGroup(cmd.Process.Pid)
	}
	cmd.WaitDelay = WaitDelayAfterKill
}

// stopProcessGroup sends the signal ladder to the group led by pid. Processes a
// CLI started in a group of their own (Claude Code's Bash tool does) are swept
// by pid at the kill step, from a list taken while the leader is still alive.
func stopProcessGroup(pid int) error {
	strays := descendantsOutsideGroup(pid)
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
		for _, stray := range strays {
			_ = syscall.Kill(stray, syscall.SIGKILL)
		}
	}()
	return nil
}

func groupAlive(pid int) bool {
	return syscall.Kill(-pid, 0) == nil
}

// descendantsOutsideGroup lists the pids under pid that left its process group.
// Best effort: it needs pgrep, and a pid can be reused before the kill step.
func descendantsOutsideGroup(pid int) []int {
	group, err := syscall.Getpgid(pid)
	if err != nil {
		return nil
	}
	var outside []int
	queue := []int{pid}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		out, err := exec.Command("pgrep", "-P", strconv.Itoa(parent)).Output()
		if err != nil {
			continue
		}
		for _, field := range strings.Fields(string(out)) {
			child, err := strconv.Atoi(field)
			if err != nil {
				continue
			}
			queue = append(queue, child)
			if childGroup, err := syscall.Getpgid(child); err == nil && childGroup != group {
				outside = append(outside, child)
			}
		}
	}
	return outside
}
