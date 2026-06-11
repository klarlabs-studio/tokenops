//go:build unix

package mcp

import (
	"os"
	"os/exec"
	"syscall"
)

// spawnDetached starts cmd as a new session leader so it is not killed
// when the MCP serve process exits, and releases the handle so no
// zombie accumulates.
func spawnDetached(exe string, args []string, logFile *os.File) (int, string, error) {
	cmd := exec.Command(exe, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, "", err
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return pid, logFile.Name(), err
	}
	return pid, logFile.Name(), nil
}
