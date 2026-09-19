package cli

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/daemon"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Install, remove, or inspect the supervised ingestion daemon",
		Long: `daemon writes a launchd LaunchAgent (macOS) or systemd user unit
(Linux) that keeps ` + "`tokenops start`" + ` alive across reboot.

` + "`tokenops serve`" + ` is the MCP server and ingests nothing.
` + "`tokenops start`" + ` runs the pollers. Nothing supervised start, so a
reboot silently ended observability while the client kept respawning
serve — that went unnoticed for 27 days. This command is the
installable form of deploy/launchd and deploy/systemd.

  tokenops daemon install            # write + load the unit
  tokenops daemon install --no-load  # write only
  tokenops daemon status
  tokenops daemon restart           # re-read config after a config change
  tokenops daemon uninstall`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newDaemonInstallCmd(), newDaemonUninstallCmd(), newDaemonStatusCmd(), newDaemonRestartCmd())
	return cmd
}

type daemonFlags struct {
	kind   string
	bin    string
	home   string
	path   string
	noLoad bool
	dryRun bool
}

func (f daemonFlags) resolve() (daemon.UnitKind, string, string, string, error) {
	kind, err := daemon.ParseUnitKind(f.kind)
	if err != nil {
		return "", "", "", "", err
	}
	bin := f.bin
	if bin == "" {
		bin = selfExe()
	}
	home := f.home
	if home == "" {
		var herr error
		home, herr = os.UserHomeDir()
		if herr != nil {
			return "", "", "", "", fmt.Errorf("home directory: %w", herr)
		}
	}
	path := f.path
	if path == "" {
		path = daemon.DefaultUnitPath(kind, home)
	}
	return kind, bin, home, path, nil
}

// applyUnit has the supervisor run the unit. A var so tests can fail it
// without a real supervisor.
var applyUnit = daemon.ApplyUnit

func newDaemonInstallCmd() *cobra.Command {
	f := daemonFlags{}
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write and load a launchd/systemd unit for tokenops start",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kind, bin, home, path, err := f.resolve()
			if err != nil {
				return err
			}
			body, err := daemon.RenderUnit(kind, bin, home)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Installing %s unit using binary:\n  %s\n", kind, bin)
			if f.dryRun {
				fmt.Fprintf(out, "\n--dry-run: would write %s\n\n%s", path, body)
				if !strings.HasSuffix(body, "\n") {
					fmt.Fprintln(out)
				}
				return nil
			}
			existing, readErr := os.ReadFile(path)
			changed := readErr != nil || !daemon.UnitEqual(string(existing), body)
			if !changed {
				fmt.Fprintf(out, "Already up to date: %s\n", path)
			} else {
				if err := daemon.WriteUnit(path, body); err != nil {
					return err
				}
				fmt.Fprintf(out, "Wrote %s\n", path)
			}
			if f.noLoad {
				fmt.Fprintln(out, "Skipped load (--no-load). Enable with:")
				fmt.Fprintln(out, "  "+enableHint(kind, path))
				return nil
			}
			if err := applyUnit(kind, path, changed); err != nil {
				// Applying the unit replaces the running daemon, so a
				// failure here can leave none at all. Exit non-zero: a
				// caller that took 0 as success would never learn
				// ingestion had stopped.
				fmt.Fprintln(out, "The daemon could not be started, so nothing is ingesting right now.")
				fmt.Fprintln(out, "Run `tokenops daemon install` again; `tokenops daemon status` shows its state.")
				return fmt.Errorf("start %s unit: %w", kind, err)
			}
			fmt.Fprintln(out, "Running. `tokenops start` will now survive reboot.")
			fmt.Fprintln(out, "Verify with: tokenops daemon status")
			return nil
		},
	}
	addDaemonFlags(cmd, &f, true)
	return cmd
}

func newDaemonUninstallCmd() *cobra.Command {
	f := daemonFlags{}
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Unload and remove the supervised ingestion unit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kind, _, _, path, err := f.resolve()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !f.noLoad && !f.dryRun {
				if err := daemon.DisableUnit(kind, path); err != nil {
					fmt.Fprintf(out, "warning: unload: %v\n", err)
				}
			}
			if f.dryRun {
				fmt.Fprintf(out, "--dry-run: would remove %s\n", path)
				return nil
			}
			if err := daemon.RemoveUnit(path); err != nil {
				return err
			}
			fmt.Fprintf(out, "Removed %s\n", path)
			return nil
		},
	}
	addDaemonFlags(cmd, &f, false)
	return cmd
}

func newDaemonStatusCmd() *cobra.Command {
	f := daemonFlags{}
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether the supervised ingestion unit is installed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kind, bin, _, path, err := f.resolve()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "This binary: %s\n", bin)
			fmt.Fprintf(out, "Supervisor:  %s (%s)\n", kind, runtime.GOOS)
			st, err := os.Stat(path)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Fprintf(out, "Unit:        not installed (%s)\n", path)
					fmt.Fprintln(out, "Next:        tokenops daemon install")
					return nil
				}
				return err
			}
			fmt.Fprintf(out, "Unit:        %s (%o)\n", path, st.Mode().Perm())
			// Name where the supervised daemon actually logs. Operators
			// otherwise follow ~/.tokenops/daemon.log, which only the MCP
			// spawn path writes and which can sit stale for months while
			// looking authoritative.
			if home, herr := os.UserHomeDir(); herr == nil {
				if lp := daemon.LogPath(kind, home); lp != "" {
					fmt.Fprintf(out, "Logs:        %s\n", lp)
				}
			}
			return nil
		},
	}
	addDaemonFlags(cmd, &f, false)
	return cmd
}

func addDaemonFlags(cmd *cobra.Command, f *daemonFlags, install bool) {
	cmd.Flags().StringVar(&f.kind, "kind", "auto", "supervisor: auto, launchd, or systemd")
	cmd.Flags().StringVar(&f.bin, "bin", "", "tokenops binary path (defaults to this executable)")
	cmd.Flags().StringVar(&f.home, "home", "", "home directory used in the unit (defaults to $HOME)")
	cmd.Flags().StringVar(&f.path, "path", "", "override unit file path")
	if install {
		cmd.Flags().BoolVar(&f.noLoad, "no-load", false, "write the unit without calling launchctl/systemctl")
		cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "print the unit without writing")
	} else {
		cmd.Flags().BoolVar(&f.noLoad, "no-load", false, "skip launchctl/systemctl")
		cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "print what would change without writing")
	}
}

func enableHint(kind daemon.UnitKind, path string) string {
	switch kind {
	case daemon.UnitLaunchd:
		return fmt.Sprintf("launchctl bootstrap gui/$(id -u) %s && launchctl kickstart -k gui/$(id -u)/%s", path, daemon.LaunchdLabel)
	case daemon.UnitSystemd:
		return "systemctl --user daemon-reload && systemctl --user enable --now " + daemon.SystemdName
	default:
		return ""
	}
}

// newDaemonRestartCmd bounces the supervised daemon so it re-reads config.
//
// The daemon loads its config once, at boot, so `plan set`, `provider set`
// and `vendor-usage enable` all end by telling the operator to restart it.
// None of them could name a command, because there was not one.
func newDaemonRestartCmd() *cobra.Command {
	f := daemonFlags{}
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the supervised ingestion daemon so it re-reads config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kind, _, _, path, err := f.resolve()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if _, serr := os.Stat(path); serr != nil {
				if os.IsNotExist(serr) {
					return fmt.Errorf("no unit installed at %s; run `tokenops daemon install`, "+
						"or restart an unsupervised daemon with Ctrl-C and `tokenops start`", path)
				}
				return serr
			}
			if err := daemon.RestartUnit(kind); err != nil {
				return err
			}
			fmt.Fprintf(out, "restarted %s (%s)\n", daemon.LaunchdLabel, kind)
			fmt.Fprintln(out, "it has re-read the config; verify with `tokenops status`")
			return nil
		},
	}
	addDaemonFlags(cmd, &f, false)
	return cmd
}
