// Package selfexe tells a running tokenops process whether the binary it
// was started from has since been replaced by an install.
//
// Homebrew installs the binary under a versioned directory
// (Caskroom/tokenops/0.54.3/tokenops) and points bin/tokenops at it. An
// upgrade repoints the link and deletes the old directory, while every
// running process keeps executing the deleted file. Nothing in the process
// notices on its own; the MCP server reports it to its client, and the
// daemon restarts itself onto the new binary.
package selfexe

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Snapshot records which binary this process was started from, so a later
// check can tell whether an install has replaced it since.
type Snapshot struct {
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

// Drift is what a check of the running binary found.
type Drift struct {
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
func SnapshotExecutable() (*Snapshot, error) {
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
	return snapshot(invoked...)
}

// snapshot resolves the first path as the running binary and keeps every
// path to re-resolve later.
func snapshot(invoked ...string) (*Snapshot, error) {
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
	return &Snapshot{running: running, info: info, invoked: invoked}, nil
}

// Check compares the install on disk now with the binary this process
// started from. Stats and readlinks only, so it is cheap enough to run on
// every status call or every poll. A nil snapshot (startup could not
// resolve the binary) reports no drift: an unknown is not evidence of a
// stale process.
func (s *Snapshot) Check() Drift {
	if s == nil {
		return Drift{}
	}
	d := Drift{Running: s.running}
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
