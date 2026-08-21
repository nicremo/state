//go:build !windows

package runner

import (
	"errors"
	"os/exec"
	"syscall"
)

// prepareProcessGroup puts the adapter process into its own process group so
// cancellation kills the whole tree, and routes context cancellation there.
func prepareProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return killProcessGroup(command)
	}
}

// killProcessGroup SIGKILLs the adapter's process group. An already-dead group
// (ESRCH) is not an error: cancellation after a natural exit is a no-op.
func killProcessGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
