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
// that stays put, and the cask's upgrade hook refreshes that copy.

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
installed copy and restart it if it is running; --refresh does the same by
hand and opens it only if it was running.`,
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
			return installMenubar(cmd.OutOrStdout(), src, dst, refresh)
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "replace the installed copy with this version's; reopen it only if it was running (the Homebrew upgrade hook)")
	return cmd
}

// installMenubar copies src over dst when src is there, and opens dst.
// With refresh it changes nothing unless dst is installed, and reopens the
// app only if it was running.
func installMenubar(out io.Writer, src, dst string, refresh bool) error {
	_, statErr := os.Stat(dst)
	installed := statErr == nil
	if refresh && !installed {
		return nil // never installed: an upgrade does not install it
	}
	if src == "" && !installed {
		return errors.New("the menu bar app is not beside this binary: it ships with the Homebrew install (brew install --cask klarlabs-studio/tap/tokenops); from a source checkout, run `make menubar`")
	}
	ops := menubarapp.System()
	running := ops.Running()
	if src != "" {
		if running {
			ops.Quit()
		}
		if err := ops.Replace(src, dst); err != nil {
			return fmt.Errorf("install %s: %w", dst, err)
		}
		fmt.Fprintf(out, "Installed %s\n", dst)
	}
	if refresh && !running {
		return nil
	}
	if err := ops.Open(dst); err != nil {
		return fmt.Errorf("open %s: %w", dst, err)
	}
	fmt.Fprintln(out, "TokenOps is in the menu bar: click the icon for the details, right-click it for Launch at Login and Alerts.")
	return nil
}
