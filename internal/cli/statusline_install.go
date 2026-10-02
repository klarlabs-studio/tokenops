package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/config"
)

// statuslineRefreshSeconds re-runs the line while Claude Code is idle, so
// a quota window that resets shows without waiting for the next turn.
const statuslineRefreshSeconds = 30

func newStatuslineInstallCmd() *cobra.Command {
	var settingsPath string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Show TokenOps' line in Claude Code's status line, keeping any you already have",
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			if err := installStatusline(cmd.OutOrStdout(), resolveSettingsPath(settingsPath), exe); err != nil {
				return err
			}
			return recordStatuslineChoice(true)
		},
	}
	cmd.Flags().StringVar(&settingsPath, "settings", "", "Claude Code settings.json (default ~/.claude/settings.json)")
	return cmd
}

func newStatuslineUninstallCmd() *cobra.Command {
	var settingsPath string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Put Claude Code's status line back as it was",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := uninstallStatusline(cmd.OutOrStdout(), resolveSettingsPath(settingsPath)); err != nil {
				return err
			}
			// Remembered, so a later init does not put it back.
			if err := recordStatuslineChoice(false); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "init will leave it out; `tokenops statusline install` brings it back")
			return nil
		},
	}
	cmd.Flags().StringVar(&settingsPath, "settings", "", "Claude Code settings.json (default ~/.claude/settings.json)")
	return cmd
}

// statuslineOriginalPath keeps the status line TokenOps replaced, so
// uninstall puts back exactly what was there.
func statuslineOriginalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tokenops", "statusline-original.json"), nil
}

// isOurStatusline reports whether a statusLine runs `tokenops statusline`.
func isOurStatusline(sl map[string]any) bool {
	command, _ := sl["command"].(string)
	return strings.Contains(command, "statusline") && strings.Contains(filepath.Base(strings.Fields(command + " x")[0]), "tokenops")
}

// installStatusline points Claude Code's statusLine at TokenOps, wrapping
// the command that was there so it keeps showing. Re-running it updates
// the binary path and changes nothing else.
func installStatusline(out io.Writer, settingsPath, exe string) error {
	settings, _, err := loadSettings(settingsPath)
	if err != nil {
		return err
	}
	current, _ := settings["statusLine"].(map[string]any)
	wrapped := ""
	if current != nil && !isOurStatusline(current) {
		// Keep the original, for uninstall and to wrap.
		path, err := statuslineOriginalPath()
		if err != nil {
			return err
		}
		b, err := json.MarshalIndent(current, "", "  ")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return err
		}
	}
	if original, ok := readOriginalStatusline(); ok {
		wrapped, _ = original["command"].(string)
	}
	command := shellQuote(exe) + " statusline"
	if wrapped != "" {
		command += " --wrap " + shellQuote(wrapped)
	}
	next := map[string]any{"type": "command", "command": command, "refreshInterval": statuslineRefreshSeconds}
	if current != nil {
		// The operator's layout settings stay theirs.
		for _, k := range []string{"padding", "hideVimModeIndicator"} {
			if v, ok := current[k]; ok {
				next[k] = v
			}
		}
	}
	if current != nil && fmt.Sprint(current) == fmt.Sprint(next) {
		fmt.Fprintln(out, "status line: already TokenOps' — no changes.")
		return nil
	}
	settings["statusLine"] = next
	// Subagent rows are TokenOps' only where the operator has none of
	// their own: the row protocol has no way to wrap another command.
	if _, theirs := settings["subagentStatusLine"]; !theirs {
		settings["subagentStatusLine"] = map[string]any{"type": "command", "command": shellQuote(exe) + " statusline subagents"}
	}
	if err := writeSettings(settingsPath, settings); err != nil {
		return err
	}
	if wrapped != "" {
		fmt.Fprintf(out, "status line: TokenOps' line, with your own (%s) under it — `tokenops statusline uninstall` restores it\n", wrapped)
	} else {
		fmt.Fprintln(out, "status line: TokenOps' line — `tokenops statusline uninstall` removes it")
	}
	return nil
}

// uninstallStatusline restores the status line TokenOps replaced, or
// removes TokenOps' when there was none.
func uninstallStatusline(out io.Writer, settingsPath string) error {
	settings, _, err := loadSettings(settingsPath)
	if err != nil {
		return err
	}
	current, _ := settings["statusLine"].(map[string]any)
	if current == nil || !isOurStatusline(current) {
		fmt.Fprintln(out, "status line: not TokenOps' — nothing to remove.")
		return nil
	}
	if original, ok := readOriginalStatusline(); ok {
		settings["statusLine"] = original
	} else {
		delete(settings, "statusLine")
	}
	if sub, _ := settings["subagentStatusLine"].(map[string]any); sub != nil && isOurStatusline(sub) {
		delete(settings, "subagentStatusLine")
	}
	if err := writeSettings(settingsPath, settings); err != nil {
		return err
	}
	if path, err := statuslineOriginalPath(); err == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	fmt.Fprintln(out, "status line: restored")
	return nil
}

func readOriginalStatusline() (map[string]any, bool) {
	path, err := statuslineOriginalPath()
	if err != nil {
		return nil, false
	}
	b, err := os.ReadFile(path) //nolint:gosec // TokenOps' own file
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil || len(m) == 0 {
		return nil, false
	}
	return m, true
}

// recordStatuslineChoice writes the operator's choice to statusline.enabled
// in TokenOps' config. A missing config is not an error: there is nothing
// for init to override yet.
func recordStatuslineChoice(on bool) error {
	path, err := config.DefaultPath()
	if err != nil {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return err
	}
	cfg.Statusline.Enabled = &on
	return writeMutableConfig(path, cfg)
}
