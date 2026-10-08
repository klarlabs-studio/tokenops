//go:build unix

package accounts

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup runs cmd in a process group of its own and, on
// cancellation, kills the whole group: a CLI's helpers must not outlive
// it, nor hold its output open.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
