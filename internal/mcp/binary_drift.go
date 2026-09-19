package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"go.klarlabs.de/tokenops/internal/version"
)

// StaleServerNextAction is the remediation appended when this MCP server is
// running a binary that has since been replaced.
//
// It names the client, not tokenops: `tokenops serve` is a child of the MCP
// client and lives exactly as long as it does. Neither `tokenops start` nor a
// daemon restart touches it, which is how one agent kept talking to 0.54.3
// for weeks after 0.66.0 was installed.
const StaleServerNextAction = "restart your MCP client (or reconnect its tokenops server, e.g. /mcp in Claude Code) so it spawns the installed 'tokenops serve' — the MCP server is a child of the client, and tokenops cannot restart it"

// ExecutableSnapshot records which binary this process was started from, so
// a later check can tell whether an install has replaced it since.
//
// Homebrew installs the binary under a versioned directory
// (Caskroom/tokenops/0.54.3/tokenops) and points bin/tokenops at it. An
// upgrade repoints the link and deletes the old directory, while every
// running `tokenops serve` keeps executing the deleted file. Nothing in the
// process notices: it answers with old code and reports its old version,
// and an operator reading that version has no reason to suspect it.
type ExecutableSnapshot struct {
	// running is the fully resolved path of the binary at startup.
	running string
	// info is the running binary's identity at startup. Comparing it later
	// catches a replacement at the same path (`go install`, `make
	// install`), which leaves every path resolving exactly as before.
	info os.FileInfo
	// invoked are the paths this process could have been launched through
	// — typically the bin/tokenops link. Re-resolving them later shows
	// where an install now points.
	invoked []string
}

// BinaryDrift is what a check of the running binary found.
type BinaryDrift struct {
	// OutOfDate reports that the binary this process runs is no longer the
	// one installed.
	OutOfDate bool
	// Running is the binary this process was started from.
	Running string
	// Installed is where the launch path resolves now, when that differs
	// from Running; empty when the binary was removed or replaced in place.
	Installed string
}

// SnapshotExecutable records this process's binary. It is cheap and never
// spawns anything: os.Executable, a PATH lookup of argv[0], and stats.
//
// Both paths are kept because platforms disagree on what os.Executable
// returns. macOS reports the path as launched (the bin/tokenops link); Linux
// reads /proc/self/exe, which is already resolved, so the link is only
// recoverable from argv[0].
func SnapshotExecutable() (*ExecutableSnapshot, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}
	invoked := []string{exe}
	if len(os.Args) > 0 && os.Args[0] != "" {
		if p, lErr := exec.LookPath(os.Args[0]); lErr == nil {
			if abs, aErr := filepath.Abs(p); aErr == nil && abs != exe {
				invoked = append(invoked, abs)
			}
		}
	}
	return snapshotExecutable(invoked...)
}

// snapshotExecutable resolves the first path as the running binary and
// keeps every path to re-resolve later.
func snapshotExecutable(invoked ...string) (*ExecutableSnapshot, error) {
	if len(invoked) == 0 {
		return nil, fmt.Errorf("no executable path")
	}
	running, err := filepath.EvalSymlinks(invoked[0])
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", invoked[0], err)
	}
	info, err := os.Stat(running)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", running, err)
	}
	return &ExecutableSnapshot{running: running, info: info, invoked: invoked}, nil
}

// Check compares the install on disk now with the binary this process
// started from. Stats and readlinks only, so it is cheap enough to run on
// every status call. A nil snapshot (startup could not resolve the binary)
// reports no drift: an unknown is not evidence of a stale server.
func (s *ExecutableSnapshot) Check() BinaryDrift {
	if s == nil {
		return BinaryDrift{}
	}
	d := BinaryDrift{Running: s.running}
	info, err := os.Stat(s.running)
	if err != nil || !os.SameFile(s.info, info) {
		d.OutOfDate = true
	}
	for _, p := range s.invoked {
		now, err := filepath.EvalSymlinks(p)
		if err != nil || now == s.running {
			continue
		}
		d.OutOfDate = true
		d.Installed = now
		break
	}
	return d
}

// Warning renders the operator-facing line, or "" when the binary is current.
func (d BinaryDrift) Warning() string {
	if !d.OutOfDate {
		return ""
	}
	installed := "has since been removed or replaced"
	if d.Installed != "" {
		installed = "has since been replaced by " + d.Installed
	}
	return fmt.Sprintf(
		"this MCP server is out of date: it is still running tokenops %s from %s, which %s — "+
			"everything installed since is invisible to this agent until the MCP client restarts the server",
		version.String(), d.Running, installed)
}

// binaryDrift runs the injected check, or reports nothing when none is wired.
func binaryDrift(d ControlDeps) BinaryDrift {
	if d.BinaryDrift == nil {
		return BinaryDrift{}
	}
	return d.BinaryDrift()
}
