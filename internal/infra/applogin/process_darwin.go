//go:build darwin

package applogin

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"

	"golang.org/x/sys/unix"
)

// listProcesses lists the command lines of this user's processes through
// sysctl (kern.proc.all, kern.procargs2): no subprocess, and another
// user's processes are not read.
func listProcesses(ctx context.Context) ([]Process, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	uid := uint32(os.Getuid()) //nolint:gosec // a uid is never negative
	var out []Process
	for _, p := range procs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if p.Eproc.Ucred.Uid != uid || p.Proc.P_pid <= 0 {
			continue
		}
		raw, err := unix.SysctlRaw("kern.procargs2", int(p.Proc.P_pid))
		if err != nil {
			continue
		}
		if args := procArgs(raw); len(args) > 0 {
			out = append(out, Process{PID: int(p.Proc.P_pid), Args: args})
		}
	}
	return out, nil
}

// procArgs parses kern.procargs2: argc, the executable path, padding, then
// argc NUL-terminated arguments.
func procArgs(raw []byte) []string {
	if len(raw) < 4 {
		return nil
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil
	}
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	var args []string
	for len(args) < argc && len(rest) > 0 {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			args = append(args, string(rest))
			break
		}
		args = append(args, string(rest[:j]))
		rest = rest[j+1:]
	}
	return args
}
