package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newHooksUninstallCmd() *cobra.Command {
	var (
		coach, readGuard, routeGuard bool
		settingsPath                 string
		dryRun                       bool
		client                       string
	)
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove only the hook entries tokenops added",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateHookClient(client); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			markers := selectedMarkers(coach, readGuard, routeGuard)

			if opencodeClient(client) {
				pdir, derr := opencodePluginDir(settingsPath)
				if derr != nil {
					return derr
				}
				return uninstallOpencodePlugin(out, pdir, markers, dryRun)
			}

			path, err := resolveHookConfigPath(client, settingsPath)
			if err != nil {
				return err
			}

			settings, existed, err := loadSettings(path)
			if err != nil {
				return err
			}
			if !existed {
				fmt.Fprintf(out, "No settings file at %s — nothing to remove.\n", path)
				return nil
			}
			hooks := hooksMap(settings)

			var removed []string
			for _, marker := range markers {
				if removeMarker(hooks, marker) {
					removed = append(removed, marker)
				}
			}
			if len(hooks) == 0 {
				delete(settings, "hooks")
			} else {
				settings["hooks"] = hooks
			}

			if len(removed) == 0 {
				fmt.Fprintln(out, "No tokenops hook entries found — nothing to remove.")
				return nil
			}
			for _, r := range removed {
				fmt.Fprintf(out, "  - %s\n", r)
			}
			if dryRun {
				fmt.Fprintln(out, "\n--dry-run: not writing.")
				return nil
			}
			if err := writeSettings(path, settings); err != nil {
				return err
			}
			fmt.Fprintf(out, "Wrote %s (backup at %s.bak)\n", path, path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&coach, "coach", false, "remove the coaching nudge")
	cmd.Flags().BoolVar(&readGuard, "read-guard", false, "remove the read guard")
	cmd.Flags().BoolVar(&routeGuard, "route-guard", false, "remove the per-turn model-fit guard")
	cmd.Flags().StringVar(&client, "client", hookClientClaudeCode, hookClientFlagUsage)
	cmd.Flags().StringVar(&settingsPath, "settings", "", "hook config path (defaults per client)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would change without writing")
	return cmd
}

// selectedMarkers maps the --coach/--read-guard/--route-guard flags to the
// verbs they name.
//
// Naming none means all of them: an uninstall with no argument reads as
// "leave nothing of yours behind", and defaulting to a subset is what let a
// full uninstall silently keep route-guard wired.
func selectedMarkers(coach, readGuard, routeGuard bool) []string {
	if !coach && !readGuard && !routeGuard {
		return hookMarkers
	}
	var out []string
	if coach {
		out = append(out, "coach-hook")
	}
	if readGuard {
		out = append(out, "read-guard")
	}
	if routeGuard {
		out = append(out, "route-guard")
	}
	return out
}
