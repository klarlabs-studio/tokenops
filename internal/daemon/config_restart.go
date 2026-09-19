package daemon

import (
	"fmt"
	"os"
)

// UnitInstalled reports whether a supervisor unit for the daemon exists on
// this machine — the one definition of "supervised" the terminal and the
// MCP tools share.
func UnitInstalled() bool {
	kind, err := DetectUnitKind()
	if err != nil {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(DefaultUnitPath(kind, home))
	return err == nil
}

// ConfigRestart is the outcome of making a config write take effect.
type ConfigRestart struct {
	Supervised bool
	Restarted  bool
	Err        error
}

// RestartForConfig restarts the supervised daemon so it re-reads config.
// The daemon reads config once, at boot: a write not followed by a restart
// has not changed anything yet.
func RestartForConfig() ConfigRestart {
	return restartForConfig(UnitInstalled(), func() error {
		kind, err := DetectUnitKind()
		if err != nil {
			return err
		}
		return RestartUnit(kind)
	})
}

func restartForConfig(supervised bool, restart func() error) ConfigRestart {
	if !supervised {
		return ConfigRestart{}
	}
	if err := restart(); err != nil {
		return ConfigRestart{Supervised: true, Err: err}
	}
	return ConfigRestart{Supervised: true, Restarted: true}
}

// Note is the one-line account of what happened, naming a command that
// exists on this machine when something is still left to do.
func (r ConfigRestart) Note() string {
	switch {
	case r.Restarted:
		return "restarted the daemon; the change is live"
	case r.Err != nil:
		return fmt.Sprintf("written, but the daemon could not be restarted (%v) — run `tokenops daemon restart`", r.Err)
	default:
		return "written; no supervised daemon here, so restart `tokenops start` where it runs " +
			"(or `tokenops daemon install` to supervise it)"
	}
}
