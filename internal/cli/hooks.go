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

	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/infra/readguard"
	"go.klarlabs.de/tokenops/internal/version"
)

// The Claude Code settings.json `hooks` schema is a map of event name →
// array of groups, each group being {matcher?, hooks:[{type,command,args,
// timeout}]}. tokenops owns exactly the command entries whose first arg is a
// known verb ("coach-hook" or "read-guard"); that verb is our marker, so we
// can install/update/remove our entries idempotently without touching any
// other hooks the operator has configured.

// hookSpec describes one tokenops hook we can wire in.
type hookSpec struct {
	name    string // human label
	event   string // Claude Code hook event (Stop, PreToolUse, ...)
	matcher string // tool matcher; "" means "all" (Stop has no matcher)
	marker  string // args[0] that identifies our entry (coach-hook / read-guard)
	args    []string
}

// hookMarkers is every verb tokenops installs, in install order, and the
// single list status and uninstall walk.
//
// It exists because route-guard shipped wired to install and invisible to
// both of the commands that inspect and remove a hook: status enumerated a
// hardcoded pair of markers, and uninstall rebuilt its specs from the
// coach/read-guard flags alone. The guard ran on every turn while status
// reported it absent, and no flag could take it out again. Adding a fourth
// guard must not be able to reintroduce that, so the three commands read
// the set from here rather than each spelling it out.
var hookMarkers = []string{"coach-hook", "read-guard", "route-guard"}

// newHooksCmd is the top-level installer/manager for tokenops Claude Code
// hooks. It merges our entries into ~/.claude/settings.json idempotently,
// backs up before writing, and can uninstall or report status.
func newHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Install, inspect or remove TokenOps' hooks in your AI clients",
		Long: `hooks wires tokenops' hooks into a client's hook config: the
end-of-turn coaching nudge (coach-hook), the file-read dedup guard
(read-guard), and the per-turn model-fit guard (route-guard). It merges
entries idempotently — re-running never duplicates — backs up the prior
config alongside it, and writes atomically.

--client selects which client to act on, and install, status and uninstall
all take it: claude-code (~/.claude/settings.json), codex
(~/.codex/hooks.json), cursor (~/.cursor/hooks.json) and opencode (a
generated plugin under ~/.config/opencode/plugins).

  tokenops hooks install --coach             # wire the end-of-turn nudge
  tokenops hooks install --read-guard        # wire the Read dedup guard
  tokenops hooks install --route-guard       # wire the model-fit guard
  tokenops hooks status                      # show what's wired here
  tokenops hooks status --client cursor      # ... and what's wired there
  tokenops hooks uninstall --route-guard     # remove one of tokenops' entries
  tokenops hooks uninstall                   # remove all of them`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newHooksInstallCmd(), newHooksUninstallCmd(), newHooksStatusCmd())
	return cmd
}

// specsFor returns the hook specs selected by the --coach/--read-guard flags.
// When neither is set, both are selected (install everything is the common
// case). budget parametrises the coach entry's args.
func specsFor(coach, readGuard bool, budget float64) []hookSpec {
	return specsForMode(coach, readGuard, budget, "")
}

// specsForMode is specsFor with the read-guard mode pinned by the caller.
// An empty guardMode writes no --mode flag at all, which is the default:
// the guard then resolves its behaviour from coaching.delivery on every
// invocation, so changing that one key takes effect immediately instead
// of requiring the hook to be re-registered. Pass a mode only to pin one
// against config — the installed flag wins, by design.
func specsForMode(coach, readGuard bool, budget float64, guardMode readguard.Mode) []hookSpec {
	return specsForModeWithRoute(coach, readGuard, false, budget, guardMode, "")
}

// specsForModeWithRoute adds the per-turn route guard, which needs to
// know whose models the client runs: the tier catalog is keyed by
// provider, and a client that reports "gpt-5.3-codex" priced against
// Anthropic's card would be placed against the wrong menu entirely.
func specsForModeWithRoute(coach, readGuard, routeGuard bool, budget float64, guardMode readguard.Mode, provider string) []hookSpec {
	if !coach && !readGuard && !routeGuard {
		coach, readGuard = true, true
	}
	var out []hookSpec
	if coach {
		out = append(out, hookSpec{
			name:   "coach-hook (Stop nudge)",
			event:  "Stop",
			marker: "coach-hook",
			args:   []string{"coach-hook", "--budget", formatBudget(budget)},
		})
	}
	if readGuard {
		out = append(out, hookSpec{
			name:    "read-guard (Read dedup)",
			event:   "PreToolUse",
			matcher: "Read",
			marker:  "read-guard",
			args:    readGuardArgs(guardMode),
		})
	}
	if routeGuard {
		args := []string{"route-guard"}
		if provider != "" {
			args = append(args, "--provider", provider)
		}
		out = append(out, hookSpec{
			name:   "route-guard (per-turn model fit)",
			event:  "UserPromptSubmit",
			marker: "route-guard",
			args:   args,
		})
		// Claude Code's Agent tool carries the subagent's model, and a
		// PreToolUse hook can move it to a cheaper one when coach.models
		// is autonomous (ADR 0006). No other client has that tool.
		if provider == "anthropic" {
			out = append(out, hookSpec{
				name:    "route-guard (subagent model)",
				event:   "PreToolUse",
				matcher: "Agent",
				marker:  "route-guard",
				args:    args,
			})
		}
	}
	return out
}

// readGuardArgs builds the installed command. No --mode means "follow
// coaching.delivery", which is what we want in settings.json: one place
// to change, no re-install to make it take effect.
func readGuardArgs(guardMode readguard.Mode) []string {
	if guardMode == "" {
		return []string{"read-guard"}
	}
	return []string{"read-guard", "--mode", string(guardMode)}
}

// providerForClient names whose models a client runs, so the route guard
// prices a turn against the right menu.
func providerForClient(client string) string {
	switch strings.ToLower(client) {
	case hookClientCodex:
		return "openai"
	case hookClientCursor:
		return "cursor"
	default:
		return "anthropic"
	}
}

// Clients whose hook config tokenops can write.
const (
	hookClientClaudeCode = "claude-code"
	hookClientCodex      = "codex"
	hookClientCursor     = "cursor"
	hookClientOpencode   = "opencode"
)

// hookClientFlagUsage is the --client help shared by install, uninstall and
// status. Shared because the install flag went on advertising
// "claude-code | codex" for two releases after cursor and opencode were
// accepted, so the only way to discover them was to read validateHookClient.
const hookClientFlagUsage = "client to act on: claude-code | codex | cursor | opencode"

func validateHookClient(c string) error {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "", hookClientClaudeCode, hookClientCodex, hookClientCursor, hookClientOpencode:
		return nil
	default:
		return fmt.Errorf("--client %q: want one of %s, %s, %s, %s",
			c, hookClientClaudeCode, hookClientCodex, hookClientCursor, hookClientOpencode)
	}
}

// resolveHookConfigPath picks the file this client reads hooks from.
//
// Codex keeps them in ~/.codex/hooks.json, in the same nested shape
// Claude Code uses inside settings.json — event, then a list of matcher
// groups, each holding command entries. That is not a coincidence: Codex
// documents its hook payloads and its block decision as matching Claude
// Code's, down to the key names, so the merge logic here transfers
// unchanged and only the destination differs.
func resolveHookConfigPath(client, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if strings.EqualFold(client, hookClientCodex) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home for ~/.codex/hooks.json: %w", err)
		}
		return filepath.Join(home, ".codex", "hooks.json"), nil
	}
	if strings.EqualFold(client, hookClientCursor) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home for ~/.cursor/hooks.json: %w", err)
		}
		return filepath.Join(home, ".cursor", "hooks.json"), nil
	}
	return resolveSettingsPath(""), nil
}

// refuseUnsupportedHook declines to install a hook that cannot work,
// instead of writing one that never fires.
//
// Codex has no file-read tool. Across 40 real rollouts every single tool
// call was exec_command, MCP, wait, write_stdin or request_user_input:
// reading a file there means running `cat`, which reaches PreToolUse as a
// shell string rather than as a path. read-guard's whole value is knowing
// the path and the file's fingerprint exactly, and a guard that has to
// guess both from shell text would block the wrong read.
//
// Writing the hook anyway and reporting success is the defect this
// codebase keeps finding in other people's tools; declining out loud is
// the only honest option until Codex grows a read tool.
func refuseUnsupportedHook(client string, readGuard bool) error {
	if readGuard && strings.EqualFold(client, hookClientCursor) {
		return errors.New(
			"read-guard cannot work on cursor: its beforeReadFile hook is observe-only — only " +
				"beforeShellExecution and beforeMCPExecution honour a permission decision, so a read " +
				"cannot be refused. Install --coach for cursor; read-guard stays claude-code only")
	}
	if readGuard && strings.EqualFold(client, hookClientCodex) {
		return errors.New(
			"read-guard cannot work on codex: it has no file-read tool, so there is no read to intervene in " +
				"(every file read is a shell command). Install --coach for codex; read-guard stays claude-code only")
	}
	return nil
}

func newHooksInstallCmd() *cobra.Command {
	var (
		coach, readGuard, routeGuard bool
		settingsPath                 string
		dryRun                       bool
		budget                       float64
		client                       string
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Merge tokenops hooks into the client's hook config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return installClientHooks(cmd.OutOrStdout(), client, settingsPath, coach, readGuard, routeGuard, dryRun, budget)
		},
	}
	cmd.Flags().BoolVar(&coach, "coach", false, "install the Stop coaching nudge")
	cmd.Flags().BoolVar(&readGuard, "read-guard", false, "install the Read dedup guard")
	cmd.Flags().BoolVar(&routeGuard, "route-guard", false, "install the per-turn model-fit guard")
	cmd.Flags().StringVar(&client, "client", hookClientClaudeCode, hookClientFlagUsage)
	cmd.Flags().StringVar(&settingsPath, "settings", "", "hook config path (defaults per client)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the result without writing")
	cmd.Flags().Float64Var(&budget, "budget", coachhook.DefaultBudgetUSD, "coach: per-session API-equivalent USD budget")
	return cmd
}

// installClientHooks merges the selected tokenops hooks into one client's
// hook config. `hooks install` and the coach presets both install through
// it, so the two cannot wire a client differently.
func installClientHooks(out io.Writer, client, settingsPath string, coach, readGuard, routeGuard, dryRun bool, budget float64) error {
	if err := validateHookClient(client); err != nil {
		return err
	}
	if err := refuseUnsupportedHook(client, readGuard); err != nil {
		return err
	}
	exe := selfExe()
	fmt.Fprintf(out, "Installing tokenops %s hooks using binary:\n  %s\n", version.String(), exe)

	// opencode has no config file to merge into: its extension
	// point is a JavaScript module, so it takes a different path
	// entirely.
	if opencodeClient(client) {
		pdir, derr := opencodePluginDir(settingsPath)
		if derr != nil {
			return derr
		}
		return installOpencodePlugin(out, pdir, exe, readGuard, coach, routeGuard, dryRun, budget)
	}

	path, err := resolveHookConfigPath(client, settingsPath)
	if err != nil {
		return err
	}
	specs := specsForModeWithRoute(coach, readGuard, routeGuard, budget, "", providerForClient(client))
	if strings.EqualFold(client, hookClientCursor) {
		return installCursorHooks(out, path, exe, specs, dryRun)
	}

	settings, _, err := loadSettings(path)
	if err != nil {
		return err
	}
	hooks := hooksMap(settings)

	var changes []string
	for _, sp := range specs {
		entry := commandEntry(exe, sp.args)
		if strings.EqualFold(client, hookClientCodex) {
			entry = codexCommandEntry(exe, sp.args)
		}
		changed, warn := mergeHook(hooks, sp.event, sp.matcher, entry, sp.marker)
		if warn != "" {
			fmt.Fprintf(out, "  warning: %s\n", warn)
		}
		if changed {
			changes = append(changes, fmt.Sprintf("%s -> event %q matcher %q", sp.name, sp.event, sp.matcher))
		}
	}
	settings["hooks"] = hooks

	if len(changes) == 0 {
		fmt.Fprintln(out, "Already up to date — no changes.")
		return nil
	}
	for _, c := range changes {
		fmt.Fprintf(out, "  + %s\n", c)
	}

	if dryRun {
		fmt.Fprintln(out, "\n--dry-run: not writing. Resulting settings.json:")
		b, _ := json.MarshalIndent(settings, "", "  ")
		fmt.Fprintln(out, string(b))
		return nil
	}
	if err := writeSettings(path, settings); err != nil {
		return err
	}
	fmt.Fprintf(out, "Wrote %s (backup at %s.bak)\n", path, path)
	writeHookTrustNote(out, client)
	return nil
}
