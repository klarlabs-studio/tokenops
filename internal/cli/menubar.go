package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

// The menu bar app ships next to the CLI in the macOS release archive.
// The Homebrew cask unpacks it into a folder named for the version, so
// running it from there would break its Launch at Login entry on every
// upgrade. `tokenops menubar` copies it to ~/Applications instead, a path
// that stays put, and the cask's upgrade hook refreshes that copy.

// menubarApp is the bundle's name.
const menubarApp = "TokenOps.app"

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
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	app := filepath.Join(filepath.Dir(exe), menubarApp)
	if st, err := os.Stat(app); err == nil && st.IsDir() {
		return app
	}
	return ""
}

func newMenubarCmd() *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use:   "menubar",
		Short: "Open TokenOps in the menu bar (macOS)",
		Long: `menubar installs the TokenOps menu bar app to ~/Applications and opens
it: the busiest plan window next to the icon, and a panel with every plan's
windows, pace and cost and the coach's findings. Its menu has Launch at
Login.

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
	running := menubarRunning()
	if src != "" {
		if running {
			_ = exec.Command("pkill", "-f", menubarApp+"/Contents/MacOS/").Run()
			time.Sleep(500 * time.Millisecond)
		}
		if err := copyApp(src, dst); err != nil {
			return fmt.Errorf("install %s: %w", dst, err)
		}
		fmt.Fprintf(out, "Installed %s\n", dst)
	}
	if refresh && !running {
		return nil
	}
	if err := exec.Command("open", dst).Run(); err != nil {
		return fmt.Errorf("open %s: %w", dst, err)
	}
	fmt.Fprintln(out, "TokenOps is in the menu bar. Its menu has Launch at Login.")
	return nil
}

// menubarRunning reports whether the app is running.
func menubarRunning() bool {
	return exec.Command("pgrep", "-f", menubarApp+"/Contents/MacOS/").Run() == nil
}

// copyApp replaces dst with a copy of the bundle src. ditto keeps the
// bundle's modes, symlinks and signature intact.
func copyApp(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".new"
	_ = os.RemoveAll(tmp)
	if out, err := exec.Command("ditto", src, tmp).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	_ = exec.Command("xattr", "-dr", "com.apple.quarantine", tmp).Run()
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
