//go:build windows

package mcp

import (
	"os"
	"os/exec"
	"syscall"
)

// spawnDetached starts cmd detached from the MCP serve console so it
// survives the parent. CREATE_NEW_PROCESS_GROUP + DETACHED_PROCESS is
// the Windows analogue of setsid.
func spawnDetached(exe string, args []string, logFile *os.File) (int, string, error) {
	cmd := exec.Command(exe, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
	}
	if err := cmd.Start(); err != nil {
		return 0, "", err
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return pid, logFile.Name(), err
	}
	return pid, logFile.Name(), nil
}
