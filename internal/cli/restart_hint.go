package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

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
	// Reads is the config file the supervised daemon reads and Written the
	// one the command wrote, set when they are not the same file: the
	// daemon is then left running, because a restart would bring it back
	// with the config it had.
	Reads, Written string
}

// realRestartDeps binds to this machine, for a command that wrote the
// config at written.
func realRestartDeps(written string) restartDeps {
	kind, unit, ok := daemon.InstalledUnit()
	deps := restartDeps{
		Supervised: ok,
		Restart:    func() error { return daemon.RestartUnit(kind, unit) },
	}
	if reads := daemon.UnitReads(kind, unit); ok && daemon.ReadsAnother(reads, written) {
		deps.Reads, deps.Written = reads, written
		if abs, err := filepath.Abs(written); err == nil {
			deps.Written = abs
		}
	}
	return deps
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
//
// written is the config file the command wrote. The daemon reads the one
// its unit gives it, which is not the shell's when --config or
// XDG_CONFIG_HOME differ: restarting it then changed nothing and said the
// change was live.
func applyRestart(w io.Writer, written string, restart, needsMCP bool) {
	applyRestartWith(w, realRestartDeps(written), restart, needsMCP)
}

func applyRestartWith(w io.Writer, deps restartDeps, restart, needsMCP bool) {
	if deps.Supervised && deps.Reads != "" {
		fmt.Fprintln(w, "note:", daemon.ConfigRestart{Supervised: true, Reads: deps.Reads, Written: deps.Written}.Note())
		if needsMCP {
			fmt.Fprintln(w, "note: restart your MCP client to reload its tokenops server")
		}
		return
	}
	// Nothing supervised means nothing to bounce. Say what to do by hand
	// rather than report a restart that did not happen.
	if !restart || !deps.Supervised {
		printRestartHintFor(w, deps.Supervised, needsMCP)
		return
	}
	progress := startActivity(w, "Restarting the supervised daemon")
	if err := deps.Restart(); err != nil {
		progress.failure("could not restart the daemon")
		fmt.Fprintf(w, "  error: %v\n  the change is written but not live — %s\n",
			err, restartHint(true, needsMCP))
		return
	}
	progress.success("restarted the daemon; the change is live")
	if needsMCP {
		fmt.Fprintln(w, "note: restart your MCP client too — its tokenops server is a child of the client, not ours to restart")
	}
}

// addNoRestartFlag registers --no-restart on a command that writes config.
func addNoRestartFlag(cmd *cobra.Command, v *bool) {
	cmd.Flags().BoolVar(v, "no-restart", false,
		"write the config without restarting the daemon; the change stays inert until it is")
}
