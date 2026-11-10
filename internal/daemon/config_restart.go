package daemon

import (
	"fmt"
	"os"
	"path/filepath"
)

// InstalledUnit is the supervisor unit for the daemon on this machine: its
// kind and its file, and whether the file is there.
func InstalledUnit() (kind UnitKind, path string, ok bool) {
	kind, err := DetectUnitKind()
	if err != nil {
		return "", "", false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", false
	}
	path = DefaultUnitPath(kind, home)
	if _, err := os.Stat(path); err != nil {
		return "", "", false
	}
	return kind, path, true
}

// UnitInstalled reports whether a supervisor unit for the daemon exists on
// this machine — the one definition of "supervised" the terminal and the
// MCP tools share.
func UnitInstalled() bool {
	_, _, ok := InstalledUnit()
	return ok
}

// ConfigRestart is the outcome of making a config write take effect.
type ConfigRestart struct {
	Supervised bool
	Restarted  bool
	Err        error
	// Reads is the config file the supervised daemon reads and Written the
	// one that was written, set when they are not the same file. The daemon
	// was not restarted: it would have come back with the config it had.
	Reads, Written string
}

// RestartForConfig restarts the supervised daemon so it re-reads the
// config written at written. The daemon reads config once, at boot: a
// write not followed by a restart has not changed anything yet.
//
// A supervised daemon that reads another file is left running: restarting
// it would report a change live that it never read.
func RestartForConfig(written string) ConfigRestart {
	kind, unit, ok := InstalledUnit()
	return restartForConfig(ok, UnitReads(kind, unit), written, func() error {
		return RestartUnit(kind, unit)
	})
}

// PlanConfigRestart is what RestartForConfig would do for a write to
// written, without doing it. will says whether it would restart the
// daemon; when it would not, the outcome says why.
func PlanConfigRestart(written string) (r ConfigRestart, will bool) {
	kind, unit, ok := InstalledUnit()
	return planConfigRestart(ok, UnitReads(kind, unit), written)
}

func planConfigRestart(supervised bool, reads, written string) (ConfigRestart, bool) {
	switch {
	case !supervised:
		return ConfigRestart{}, false
	case ReadsAnother(reads, written):
		if abs, err := filepath.Abs(written); err == nil {
			written = abs
		}
		return ConfigRestart{Supervised: true, Reads: reads, Written: written}, false
	}
	return ConfigRestart{Supervised: true}, true
}

// ReadsAnother reports whether a daemon reading the config at reads does
// not read the one at written. A path that is not known ("") never counts
// as another file.
func ReadsAnother(reads, written string) bool {
	return reads != "" && written != "" && !SameFile(reads, written)
}

func restartForConfig(supervised bool, reads, written string, restart func() error) ConfigRestart {
	r, will := planConfigRestart(supervised, reads, written)
	if !will {
		return r
	}
	if err := restart(); err != nil {
		r.Err = err
		return r
	}
	r.Restarted = true
	return r
}

// Note is the one-line account of what happened, naming a command that
// exists on this machine when something is still left to do.
func (r ConfigRestart) Note() string {
	switch {
	case r.Restarted:
		return "restarted the daemon; the change is live"
	case r.Err != nil:
		return fmt.Sprintf("written, but the daemon could not be restarted (%v) — run `tokenops daemon restart`", r.Err)
	case r.Reads != "":
		return fmt.Sprintf("written to %s, which the supervised daemon does not read (it reads %s), so it was not restarted "+
			"and the change is not live — `tokenops daemon install --config %s` has it read this file",
			r.Written, r.Reads, r.Written)
	default:
		return "written; no supervised daemon here, so restart `tokenops start` where it runs " +
			"(or `tokenops daemon install` to supervise it)"
	}
}
