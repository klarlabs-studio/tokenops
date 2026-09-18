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

// printRestartHint writes the hint for this machine.
func printRestartHint(w io.Writer, needsMCP bool) {
	fmt.Fprintf(w, "next: %s\n", restartHint(daemonSupervised(), needsMCP))
}

// maybeRestart performs the restart when the operator asked for it with
// --restart, and otherwise prints what they would have to run.
//
// Opt-in rather than automatic: restarting is a side effect on a running
// service, and a command whose job is to write one config key should not
// bounce a daemon unless it was told to.
func maybeRestart(w io.Writer, restart, needsMCP bool) {
	if !restart {
		printRestartHint(w, needsMCP)
		return
	}
	kind, err := daemon.DetectUnitKind()
	if err != nil {
		fmt.Fprintf(w, "--restart: no supervisor detected (%v)\n  %s\n", err, restartHint(false, needsMCP))
		return
	}
	if !daemonSupervised() {
		fmt.Fprintf(w, "--restart: no unit installed, nothing to restart\n  %s\n", restartHint(false, needsMCP))
		return
	}
	if err := daemon.RestartUnit(kind); err != nil {
		fmt.Fprintf(w, "--restart failed: %v\n  %s\n", err, restartHint(true, needsMCP))
		return
	}
	fmt.Fprintln(w, "restarted the daemon; it has re-read the config")
	if needsMCP {
		fmt.Fprintln(w, "note: restart your MCP client too — its tokenops server is a child of the client, not ours to restart")
	}
}

// addRestartFlag registers --restart on a command that writes config.
func addRestartFlag(cmd *cobra.Command, v *bool) {
	cmd.Flags().BoolVar(v, "restart", false,
		"restart the daemon afterwards so it re-reads the config (needs an installed unit)")
}
