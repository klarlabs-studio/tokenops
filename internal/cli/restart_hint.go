package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/daemon"
)

// The daemon and the MCP server both read config once, at boot. Every
// command that writes config therefore has to say "restart" — and until
// `tokenops daemon restart` existed, none of them could name a command to
// do it with. Six sites said "restart the daemon" and left the operator to
// work out how.

// daemonSupervised reports whether this machine has a unit installed, so a
// hint can name the command that exists here rather than one that does not.
func daemonSupervised() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	// The unit step is already the one place that decides "installed" —
	// a second stat here would be a second definition to drift.
	return !daemonUnitStep(daemonUnitPath(home)).Manual
}

// restartHint renders what the operator has to do for a config change to
// take effect.
//
// needsMCP is separate because the two halves are not equivalent: the daemon
// is ours to restart, while the MCP server is a child of the client that
// spawned it. tokenops cannot restart someone else's process, so it asks for
// the client rather than offering a command that would not work.
func restartHint(supervised, needsMCP bool) string {
	daemonPart := "restart the daemon: `tokenops daemon restart`"
	if !supervised {
		daemonPart = "restart the daemon: Ctrl-C where `tokenops start` is running, then re-run it " +
			"(or `tokenops daemon install` to supervise it)"
	}
	if needsMCP {
		return daemonPart + ", and restart your MCP client to reload its tokenops server"
	}
	return daemonPart
}

// printRestartHintFor writes what the operator still has to do.
func printRestartHintFor(w io.Writer, supervised, needsMCP bool) {
	fmt.Fprintf(w, "next: %s\n", restartHint(supervised, needsMCP))
}

// restartDeps is what performing a restart depends on, injected so the
// policy is testable without bouncing the developer's own daemon.
type restartDeps struct {
	// Supervised reports whether a unit is installed to restart.
	Supervised bool
	// Restart performs it.
	Restart func() error
}

// realRestartDeps binds to this machine.
func realRestartDeps() restartDeps {
	return restartDeps{
		Supervised: daemonSupervised(),
		Restart: func() error {
			kind, err := daemon.DetectUnitKind()
			if err != nil {
				return err
			}
			return daemon.RestartUnit(kind)
		},
	}
}

// applyRestart makes a config write take effect.
//
// The daemon reads config once, at boot, so a write that is not followed by
// a restart has not changed anything yet. Printing "next: restart the
// daemon" and stopping there left the operator with a command that reported
// success and did nothing until they ran a second one — the same
// silent-no-op shape this codebase keeps finding elsewhere. So it restarts,
// and --no-restart is there for someone writing several keys in a row who
// does not want a bounce per key.
func applyRestart(w io.Writer, restart, needsMCP bool) {
	applyRestartWith(w, realRestartDeps(), restart, needsMCP)
}

func applyRestartWith(w io.Writer, deps restartDeps, restart, needsMCP bool) {
	// Nothing supervised means nothing to bounce. Say what to do by hand
	// rather than report a restart that did not happen.
	if !restart || !deps.Supervised {
		printRestartHintFor(w, deps.Supervised, needsMCP)
		return
	}
	if err := deps.Restart(); err != nil {
		fmt.Fprintf(w, "could not restart the daemon: %v\n  the change is written but not live — %s\n",
			err, restartHint(true, needsMCP))
		return
	}
	fmt.Fprintln(w, "restarted the daemon; the change is live")
	if needsMCP {
		fmt.Fprintln(w, "note: restart your MCP client too — its tokenops server is a child of the client, not ours to restart")
	}
}

// addNoRestartFlag registers --no-restart on a command that writes config.
func addNoRestartFlag(cmd *cobra.Command, v *bool) {
	cmd.Flags().BoolVar(v, "no-restart", false,
		"write the config without restarting the daemon; the change stays inert until it is")
}
