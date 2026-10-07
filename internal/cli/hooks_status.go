package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/infra/opencodeplugin"
	"go.klarlabs.de/tokenops/internal/version"
)

func newHooksStatusCmd() *cobra.Command {
	var settingsPath, client string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show which tokenops hooks are wired and the binary they call",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateHookClient(client); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			exe := selfExe()
			if jsonOut {
				st, err := hooksStatusOf(client, settingsPath, exe)
				if err != nil {
					return err
				}
				return writeControlJSON(cmd, st)
			}
			fmt.Fprintf(out, "This binary: tokenops %s\n  %s\n", version.String(), exe)

			if opencodeClient(client) {
				pdir, derr := opencodePluginDir(settingsPath)
				if derr != nil {
					return derr
				}
				return statusOpencodePlugin(out, pdir, exe)
			}

			path, err := resolveHookConfigPath(client, settingsPath)
			if err != nil {
				return err
			}
			settings, existed, err := loadSettings(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Client: %s\n  %s\n", hookClientName(client), path)
			if !existed {
				fmt.Fprintf(out, "No hook config at %s — no hooks wired.\n", path)
				return nil
			}
			hooks := hooksMap(settings)
			found := 0
			for _, marker := range hookMarkers {
				for _, loc := range findMarkerEntries(hooks, marker) {
					found++
					fmt.Fprintf(out, "  %s  event=%s matcher=%q -> %s\n", marker, loc.event, loc.matcher, loc.command)
					if !commandRunsExe(loc.command, exe) {
						fmt.Fprintf(out, "    note: points at a different binary than this one\n")
					}
				}
			}
			if found == 0 {
				fmt.Fprintf(out, "No tokenops hooks wired in %s.\n", path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&client, "client", hookClientClaudeCode, hookClientFlagUsage)
	cmd.Flags().StringVar(&settingsPath, "settings", "", "hook config path (defaults per client)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

// hooksStatus is `tokenops hooks status --json`.
type hooksStatus struct {
	Binary string      `json:"binary"`
	Client string      `json:"client"`
	Config string      `json:"config"`
	Hooks  []wiredHook `json:"hooks"`
}

// wiredHook is one TokenOps hook in a client's config.
type wiredHook struct {
	Hook    string `json:"hook"`
	Event   string `json:"event"`
	Matcher string `json:"matcher,omitempty"`
	Command string `json:"command"`
	// ThisBinary is false when the hook calls another tokenops binary.
	ThisBinary bool `json:"this_binary"`
}

// hooksStatusOf reads which TokenOps hooks client's config wires.
func hooksStatusOf(client, settingsPath, exe string) (hooksStatus, error) {
	st := hooksStatus{Binary: exe, Client: hookClientName(client), Hooks: []wiredHook{}}
	if opencodeClient(client) {
		dir, err := opencodePluginDir(settingsPath)
		if err != nil {
			return st, err
		}
		st.Config = filepath.Join(dir, opencodePluginName)
		b, err := os.ReadFile(st.Config) //nolint:gosec // a path the operator named
		if os.IsNotExist(err) {
			return st, nil
		}
		if err != nil {
			return st, err
		}
		src := string(b)
		pluginExe := opencodeplugin.Exe(src)
		for _, marker := range hookMarkers {
			if opencodeplugin.Has(src, marker) {
				st.Hooks = append(st.Hooks, wiredHook{Hook: marker, Event: opencodeplugin.Events[marker], Command: pluginExe, ThisBinary: pluginExe == exe})
			}
		}
		return st, nil
	}
	path, err := resolveHookConfigPath(client, settingsPath)
	if err != nil {
		return st, err
	}
	st.Config = path
	settings, existed, err := loadSettings(path)
	if err != nil || !existed {
		return st, err
	}
	hooks := hooksMap(settings)
	for _, marker := range hookMarkers {
		for _, loc := range findMarkerEntries(hooks, marker) {
			st.Hooks = append(st.Hooks, wiredHook{Hook: marker, Event: loc.event, Matcher: loc.matcher, Command: loc.command, ThisBinary: commandRunsExe(loc.command, exe)})
		}
	}
	return st, nil
}

// commandRunsExe reports whether a wired entry calls this binary.
//
// Claude Code stores the path alone; Codex and Cursor store the whole
// invocation, so a plain equality check would flag every one of their
// entries as pointing elsewhere and send an operator chasing a binary
// mismatch that does not exist.
func commandRunsExe(command, exe string) bool {
	if command == exe {
		return true
	}
	fields := splitCommandLine(command)
	return len(fields) > 0 && fields[0] == exe
}

// hookClientName renders the client for status output, naming the default
// rather than printing an empty string.
func hookClientName(client string) string {
	if client == "" {
		return hookClientClaudeCode
	}
	return strings.ToLower(client)
}
