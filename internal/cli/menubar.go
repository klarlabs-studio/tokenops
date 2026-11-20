package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/infra/menubarapp"
)

// The menu bar app ships next to the CLI in the macOS release archive.
// The Homebrew cask unpacks it into a folder named for the version, so
// running it from there would break its Launch at Login entry on every
// upgrade. `tokenops menubar` copies it to ~/Applications instead, a path
// that stays put, and the daemon refreshes that copy when it starts on a
// new binary (internal/daemon/menubar_refresh.go).

// menubarApp is the bundle's name.
const menubarApp = menubarapp.AppName

// menubarProcess matches the running app's command line for pgrep -f and
// pkill -f (an extended regex): the bundle's own binary only, named for
// the app by its packager. The bundle also holds the presence helper
// (Contents/MacOS/tokenops-presence), which a refresh must never kill:
// it may be holding the person's Touch ID dialog for an approval.
const menubarProcess = menubarapp.ProcessPattern

// menubarInstalled is where `tokenops menubar` keeps the app.
func menubarInstalled() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Applications", menubarApp), nil
}

// menubarShipped is the app beside this binary, following the symlink
// Homebrew puts on PATH; empty when it is not there.
func menubarShipped() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return menubarapp.ShippedBeside(exe)
}

func newMenubarCmd() *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use:   "menubar",
		Short: "Open TokenOps in the menu bar (macOS)",
		Long: `menubar installs the TokenOps menu bar app to ~/Applications and opens
it: an icon with two rings for the busiest plan, its week outside and its
session inside, each filled to the share left, and, when you click it, a
panel with every plan's windows, pace, usage and the coach's findings.
It alerts when a window drops to 20% left, to 5%, and when it is used up,
saying when it resets and, while it is still running, when it runs out at
this pace; a window that alerted says so again when it resets. Right-click it for Launch at Login and Alerts.

The app ships with the Homebrew install on macOS. Upgrades refresh the
installed copy and reopen it when it was running or you keep it in the menu
bar (you did not quit it, or Launch at Login is on); --refresh does the same
by hand. Without --refresh the command always ends with the app open.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runtime.GOOS != "darwin" {
				return errors.New("the menu bar app is macOS only; the daemon API serves the same view on every platform (GET /api/glance)")
			}
			src := menubarShipped()
			dst, err := menubarInstalled()
			if err != nil {
				return err
			}
			return installMenubar(cmd.OutOrStdout(), src, dst, refresh, menubarapp.System())
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "replace the installed copy with this version's; reopen it only if it was running or is kept in the menu bar (what an upgrade does)")
	return cmd
}

// installMenubar puts the app at dst and opens it. With refresh it does
// what an upgrade does: nothing unless dst is installed, and a reopen only
// when the app was running or the person keeps it in the menu bar.
func installMenubar(out io.Writer, src, dst string, refresh bool, ops menubarapp.Ops) error {
	if refresh {
		_, err := menubarapp.Refresh(src, dst, ops)
		return err
	}
	res, err := menubarapp.Install(src, dst, ops)
	if err != nil {
		return err
	}
	if res.Refreshed {
		fmt.Fprintf(out, "Installed %s\n", dst)
	}
	fmt.Fprintln(out, "TokenOps is in the menu bar: click the icon for the details, right-click it for Launch at Login and Alerts.")
	return nil
}
