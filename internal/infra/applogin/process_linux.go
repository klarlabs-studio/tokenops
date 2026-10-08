//go:build linux

package applogin

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// listProcesses lists the command lines of this user's processes from
// /proc; another user's are not read.
func listProcesses(ctx context.Context) ([]Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	uid := os.Getuid()
	var out []Process
	for _, e := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		dir := filepath.Join("/proc", e.Name())
		fi, err := os.Stat(dir)
		if err != nil {
			continue
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "cmdline")) // #nosec G304 -- /proc/<pid>/cmdline
		if err != nil || len(raw) == 0 {
			continue
		}
		var args []string
		for _, a := range bytes.Split(bytes.TrimRight(raw, "\x00"), []byte{0}) {
			args = append(args, string(a))
		}
		out = append(out, Process{PID: pid, Args: args})
	}
	return out, nil
}
