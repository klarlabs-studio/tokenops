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

// newHooksCmd is the top-level installer/manager for tokenops Claude Code
// hooks. It merges our entries into ~/.claude/settings.json idempotently,
// backs up before writing, and can uninstall or report status.
func newHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Install, remove, or inspect tokenops Claude Code hooks",
		Long: `hooks wires tokenops' Claude Code hooks into your settings.json:
the Stop-hook coaching nudge (coach-hook) and the PreToolUse read dedup guard
(read-guard). It merges entries idempotently — re-running never duplicates —
backs up the prior settings to settings.json.bak, and writes atomically.

  tokenops hooks install --coach            # wire the Stop coaching nudge
  tokenops hooks install --read-guard       # wire the Read dedup guard
  tokenops hooks install --coach --read-guard
  tokenops hooks status                      # show what's wired
  tokenops hooks uninstall --coach           # remove only tokenops' entries`,
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
	if !coach && !readGuard {
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
	return out
}

func newHooksInstallCmd() *cobra.Command {
	var (
		coach, readGuard bool
		settingsPath     string
		dryRun           bool
		budget           float64
		client           string
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Merge tokenops hooks into the client's hook config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateHookClient(client); err != nil {
				return err
			}
			if err := refuseUnsupportedHook(client, readGuard); err != nil {
				return err
			}
			path, err := resolveHookConfigPath(client, settingsPath)
			if err != nil {
				return err
			}
			exe := selfExe()
			out := cmd.OutOrStdout()

			fmt.Fprintf(out, "Installing tokenops %s hooks using binary:\n  %s\n", version.String(), exe)

			settings, _, err := loadSettings(path)
			if err != nil {
				return err
			}
			hooks := hooksMap(settings)

			specs := specsFor(coach, readGuard, budget)
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
		},
	}
	cmd.Flags().BoolVar(&coach, "coach", false, "install the Stop coaching nudge")
	cmd.Flags().BoolVar(&readGuard, "read-guard", false, "install the Read dedup guard")
	cmd.Flags().StringVar(&client, "client", hookClientClaudeCode, "client to arm: claude-code | codex")
	cmd.Flags().StringVar(&settingsPath, "settings", "", "hook config path (defaults per client)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the result without writing")
	cmd.Flags().Float64Var(&budget, "budget", coachhook.DefaultBudgetUSD, "coach: per-session API-equivalent USD budget")
	return cmd
}

func newHooksUninstallCmd() *cobra.Command {
	var (
		coach, readGuard bool
		settingsPath     string
		dryRun           bool
	)
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove only the hook entries tokenops added",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := resolveSettingsPath(settingsPath)
			out := cmd.OutOrStdout()

			settings, existed, err := loadSettings(path)
			if err != nil {
				return err
			}
			if !existed {
				fmt.Fprintf(out, "No settings file at %s — nothing to remove.\n", path)
				return nil
			}
			hooks := hooksMap(settings)

			specs := specsFor(coach, readGuard, coachhook.DefaultBudgetUSD)
			var removed []string
			for _, sp := range specs {
				if removeHook(hooks, sp.event, sp.marker) {
					removed = append(removed, sp.name)
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
	cmd.Flags().StringVar(&settingsPath, "settings", "", "settings.json path (defaults to ~/.claude/settings.json)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would change without writing")
	return cmd
}

func newHooksStatusCmd() *cobra.Command {
	var settingsPath string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show which tokenops hooks are wired and the binary they call",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := resolveSettingsPath(settingsPath)
			out := cmd.OutOrStdout()
			exe := selfExe()
			fmt.Fprintf(out, "This binary: tokenops %s\n  %s\n", version.String(), exe)

			settings, existed, err := loadSettings(path)
			if err != nil {
				return err
			}
			if !existed {
				fmt.Fprintf(out, "No settings file at %s — no hooks wired.\n", path)
				return nil
			}
			hooks := hooksMap(settings)
			found := 0
			for _, marker := range []string{"coach-hook", "read-guard"} {
				for _, loc := range findMarkerEntries(hooks, marker) {
					found++
					fmt.Fprintf(out, "  %s  event=%s matcher=%q -> %s\n", marker, loc.event, loc.matcher, loc.command)
					if loc.command != exe {
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
	cmd.Flags().StringVar(&settingsPath, "settings", "", "settings.json path (defaults to ~/.claude/settings.json)")
	return cmd
}

// --- settings.json plumbing ------------------------------------------------

func resolveSettingsPath(override string) string {
	if override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".claude", "settings.json")
	}
	return filepath.Join(home, ".claude", "settings.json")
}

func selfExe() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "tokenops"
}

// loadSettings reads and decodes settings.json. A missing file yields an empty
// object with existed=false; a present-but-empty file also yields {}. Malformed
// JSON is a real error — we refuse to clobber a file we can't parse.
func loadSettings(path string) (map[string]any, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, false, nil
		}
		return nil, false, fmt.Errorf("read settings %q: %w", path, err)
	}
	if len(trimSpace(b)) == 0 {
		return map[string]any{}, true, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false, fmt.Errorf("parse settings %q: %w (refusing to overwrite)", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, true, nil
}

// hooksMap returns settings["hooks"] as a map, creating/normalising it.
func hooksMap(settings map[string]any) map[string]any {
	if h, ok := settings["hooks"].(map[string]any); ok {
		return h
	}
	return map[string]any{}
}

// commandEntry builds a Claude Code command-hook entry.
// codexCommandEntry renders a hook as a single command string.
//
// Codex documents a command hook as `{"type":"command","command":"..."}`
// with no separate argument list. tokenops' Claude Code entries carry
// `args` alongside `command`, which Claude Code honours; assuming Codex
// does the same would, if wrong, run the bare binary with no subcommand —
// a hook that is installed, reports success and does nothing.
func codexCommandEntry(exe string, args []string) map[string]any {
	parts := append([]string{shellQuote(exe)}, args...)
	return map[string]any{
		"type":    "command",
		"command": strings.Join(parts, " "),
		"timeout": float64(10),
	}
}

// shellQuote wraps a path holding spaces so the command string survives
// being split by a shell.
func shellQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"'") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func commandEntry(exe string, args []string) map[string]any {
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}
	return map[string]any{
		"type":    "command",
		"command": exe,
		"args":    anyArgs,
		"timeout": float64(10),
	}
}

// mergeHook idempotently inserts entry into hooks[event] under the group with
// the given matcher. If a tokenops entry (identified by marker) already exists
// in that group it is updated in place (and any command-path change is
// surfaced as a warning); otherwise the entry is appended to the group, or a
// new group is created. Returns whether anything changed and an optional
// warning string.
func mergeHook(hooks map[string]any, event, matcher string, entry map[string]any, marker string) (bool, string) {
	groups, _ := hooks[event].([]any)
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok || matcherOf(gm) != matcher {
			continue
		}
		hlist, _ := gm["hooks"].([]any)
		for i, h := range hlist {
			hm, ok := h.(map[string]any)
			if !ok || !isMarkerEntry(hm, marker) {
				continue
			}
			warn := ""
			if oldCmd, _ := hm["command"].(string); oldCmd != entry["command"] {
				warn = fmt.Sprintf("%s already wired to %q; updating to this binary", marker, oldCmd)
			}
			if entriesEqual(hm, entry) {
				return false, ""
			}
			hlist[i] = entry
			gm["hooks"] = hlist
			return true, warn
		}
		// Group exists for this matcher but has no tokenops entry: append.
		gm["hooks"] = append(hlist, entry)
		return true, ""
	}
	// No group for this matcher: create one.
	group := map[string]any{"hooks": []any{entry}}
	if matcher != "" {
		group["matcher"] = matcher
	}
	hooks[event] = append(groups, group)
	return true, ""
}

// removeHook deletes every tokenops entry (matching marker) from hooks[event],
// pruning emptied groups and the event key. Returns whether anything was
// removed.
func removeHook(hooks map[string]any, event, marker string) bool {
	groups, _ := hooks[event].([]any)
	if len(groups) == 0 {
		return false
	}
	removed := false
	keptGroups := groups[:0]
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			keptGroups = append(keptGroups, g)
			continue
		}
		hlist, _ := gm["hooks"].([]any)
		keptHooks := hlist[:0]
		for _, h := range hlist {
			hm, ok := h.(map[string]any)
			if ok && isMarkerEntry(hm, marker) {
				removed = true
				continue
			}
			keptHooks = append(keptHooks, h)
		}
		if len(keptHooks) == 0 {
			continue // drop empty group
		}
		gm["hooks"] = keptHooks
		keptGroups = append(keptGroups, gm)
	}
	if len(keptGroups) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = keptGroups
	}
	return removed
}

// markerLoc is where a tokenops entry was found, for status output.
type markerLoc struct {
	event   string
	matcher string
	command string
}

func findMarkerEntries(hooks map[string]any, marker string) []markerLoc {
	var out []markerLoc
	for event, raw := range hooks {
		groups, _ := raw.([]any)
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			if !ok {
				continue
			}
			hlist, _ := gm["hooks"].([]any)
			for _, h := range hlist {
				hm, ok := h.(map[string]any)
				if !ok || !isMarkerEntry(hm, marker) {
					continue
				}
				cmdStr, _ := hm["command"].(string)
				out = append(out, markerLoc{event: event, matcher: matcherOf(gm), command: cmdStr})
			}
		}
	}
	return out
}

func matcherOf(group map[string]any) string {
	s, _ := group["matcher"].(string)
	return s
}

// isMarkerEntry reports whether a hook entry is one tokenops owns: a command
// hook whose first arg is our verb (coach-hook / read-guard).
func isMarkerEntry(entry map[string]any, marker string) bool {
	if t, _ := entry["type"].(string); t != "command" {
		return false
	}
	args, _ := entry["args"].([]any)
	if len(args) == 0 {
		return false
	}
	first, _ := args[0].(string)
	return first == marker
}

// entriesEqual compares two command entries by their observable fields. It
// tolerates the []string vs []any representation of args (as-built vs decoded
// from JSON).
func entriesEqual(a, b map[string]any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// writeSettings backs up the current file to <path>.bak (if it exists) then
// writes settings atomically via a temp file + rename.
func writeSettings(path string, settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}
	if prior, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(path+".bak", prior, 0o644); err != nil { //nolint:gosec // config, not a secret
			return fmt.Errorf("write backup: %w", err)
		}
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil { //nolint:gosec // config, not a secret
		return fmt.Errorf("write settings: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename settings: %w", err)
	}
	return nil
}

// trimSpace strips leading/trailing ASCII whitespace without pulling bytes for
// one call site.
func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && isSpace(b[start]) {
		start++
	}
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// readGuardArgs builds the installed command. No --mode means "follow
// coaching.delivery", which is what we want in settings.json: one place
// to change, no re-install to make it take effect.
func readGuardArgs(guardMode readguard.Mode) []string {
	if guardMode == "" {
		return []string{"read-guard"}
	}
	return []string{"read-guard", "--mode", string(guardMode)}
}

// Clients whose hook config tokenops can write.
const (
	hookClientClaudeCode = "claude-code"
	hookClientCodex      = "codex"
)

func validateHookClient(c string) error {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "", hookClientClaudeCode, hookClientCodex:
		return nil
	default:
		return fmt.Errorf("--client %q: want %s or %s", c, hookClientClaudeCode, hookClientCodex)
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
	if readGuard && strings.EqualFold(client, hookClientCodex) {
		return errors.New(
			"read-guard cannot work on codex: it has no file-read tool, so there is no read to intervene in " +
				"(every file read is a shell command). Install --coach for codex; read-guard stays claude-code only")
	}
	return nil
}

// writeHookTrustNote says what the operator still has to do.
//
// Codex skips a non-managed hook until its exact definition has been
// reviewed and trusted, and it does so silently. An installer that writes
// the file, prints "Wrote …" and stops would be reporting success for a
// hook that never runs — which is precisely the shape this tool exists to
// catch.
func writeHookTrustNote(out io.Writer, client string) {
	if !strings.EqualFold(client, hookClientCodex) {
		return
	}
	fmt.Fprintln(out, "\nNot armed yet. Codex skips a hook until you trust it:")
	fmt.Fprintln(out, "  run `/hooks` in Codex, review the tokenops entry, and trust it.")
	fmt.Fprintln(out, "Until then the hook is written but silently not run.")
}
