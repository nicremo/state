//go:build windows

package runner

import "os/exec"

// prepareProcessGroup is a no-op on Windows: the default CommandContext
// cancellation kills the process directly.
func prepareProcessGroup(command *exec.Cmd) {
}

// killProcessGroup terminates the adapter process. Windows has no POSIX
// process groups; the direct child is the best available target.
func killProcessGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}
