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
		Short: "Install, remove, or inspect tokenops client hooks",
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
	}
	return out
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
			if err := validateHookClient(client); err != nil {
				return err
			}
			if err := refuseUnsupportedHook(client, readGuard); err != nil {
				return err
			}
			exe := selfExe()
			out := cmd.OutOrStdout()
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

func newHooksStatusCmd() *cobra.Command {
	var settingsPath, client string
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
	return cmd
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

// eventEntries decomposes one element of an event's list into the hook
// entries it holds.
//
// Claude Code and Codex nest entries under a group carrying an optional
// matcher; Cursor puts the entry straight in the list. Returning the group
// (nil when the shape is flat) is what lets a caller write a filtered list
// back into the right place without knowing which client wrote the file.
func eventEntries(elem any) (group map[string]any, matcher string, entries []any) {
	m, ok := elem.(map[string]any)
	if !ok {
		return nil, "", nil
	}
	if hlist, nested := m["hooks"].([]any); nested {
		return m, matcherOf(m), hlist
	}
	return nil, "", []any{m}
}

// removeMarker deletes every tokenops entry carrying marker, across every
// event, pruning emptied groups and event keys. Returns whether anything was
// removed.
//
// It scans all events rather than taking one, because the event a guard is
// wired to is a per-client fact — Cursor spells the prompt hook
// "beforeSubmitPrompt" where Claude Code spells it "UserPromptSubmit" — and
// an uninstall that looked only where the Claude Code installer writes would
// leave the other clients' entries in place while reporting them removed.
func removeMarker(hooks map[string]any, marker string) bool {
	removed := false
	for event, raw := range hooks {
		list, _ := raw.([]any)
		if len(list) == 0 {
			continue
		}
		kept := make([]any, 0, len(list))
		for _, elem := range list {
			group, _, entries := eventEntries(elem)
			if group == nil {
				if em, ok := elem.(map[string]any); ok && isMarkerEntry(em, marker) {
					removed = true
					continue
				}
				kept = append(kept, elem)
				continue
			}
			keptEntries := make([]any, 0, len(entries))
			for _, h := range entries {
				if hm, ok := h.(map[string]any); ok && isMarkerEntry(hm, marker) {
					removed = true
					continue
				}
				keptEntries = append(keptEntries, h)
			}
			if len(keptEntries) == 0 {
				continue // drop the emptied group
			}
			group["hooks"] = keptEntries
			kept = append(kept, group)
		}
		if len(kept) == 0 {
			delete(hooks, event)
			continue
		}
		hooks[event] = kept
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
		list, _ := raw.([]any)
		for _, elem := range list {
			_, matcher, entries := eventEntries(elem)
			for _, h := range entries {
				hm, ok := h.(map[string]any)
				if !ok || !isMarkerEntry(hm, marker) {
					continue
				}
				cmdStr, _ := hm["command"].(string)
				out = append(out, markerLoc{event: event, matcher: matcher, command: cmdStr})
			}
		}
	}
	return out
}

func matcherOf(group map[string]any) string {
	s, _ := group["matcher"].(string)
	return s
}

// isMarkerEntry reports whether a hook entry is one tokenops owns.
//
// Three on-disk shapes reach here and only the first carries the verb in
// args[0]. Claude Code takes `command` plus an `args` array; Codex takes the
// whole invocation as one `command` string with type "command"; Cursor takes
// the same string with no `type` key at all. A matcher that knew only the
// Claude Code shape answered "no hooks wired" for two clients whose files
// had them — so match args[0] when there is an args array, and the command
// line's verb when there is not.
func isMarkerEntry(entry map[string]any, marker string) bool {
	if t, _ := entry["type"].(string); t != "" && t != "command" {
		return false
	}
	if args, _ := entry["args"].([]any); len(args) > 0 {
		first, _ := args[0].(string)
		return first == marker
	}
	cmd, _ := entry["command"].(string)
	return cmd != "" && commandVerb(cmd) == marker
}

// commandVerb returns the tokenops subcommand in a one-string invocation,
// i.e. the token after the binary path.
//
// It cannot be strings.Fields(cmd)[1]: the writers quote a binary path
// holding spaces, so "'/Applications/My Tools/tokenops' route-guard" would
// report "Tools/tokenops'" and the entry would be invisible to status and
// survive an uninstall — on exactly the machines whose paths have spaces.
func commandVerb(cmd string) string {
	fields := splitCommandLine(cmd)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// splitCommandLine splits on whitespace, treating a single-quoted run as one
// token. Single quotes are all shellQuote ever emits, so a full shell lexer
// would be answering a question nothing asks.
func splitCommandLine(s string) []string {
	var (
		out     []string
		cur     strings.Builder
		quoted  bool
		started bool
	)
	flush := func() {
		if started {
			out = append(out, cur.String())
			cur.Reset()
			started = false
		}
	}
	for _, r := range s {
		switch {
		case r == '\'':
			quoted = !quoted
			started = true
		case !quoted && (r == ' ' || r == '\t'):
			flush()
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	flush()
	return out
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

// installCursorHooks writes ~/.cursor/hooks.json.
//
// Cursor's schema is FLAT where Claude Code's and Codex's are nested:
// an event maps straight to a list of entries carrying `command` and
// `matcher`, with no inner "hooks" array, and the document needs a
// top-level "version". Reusing the nested writer would produce a file
// Cursor parses without error and never acts on — installed, reporting
// success, doing nothing.
//
// Event names are lower-camel too: "stop", not "Stop".
func installCursorHooks(out io.Writer, path, exe string, specs []hookSpec, dryRun bool) error {
	doc, _, err := loadSettings(path)
	if err != nil {
		return err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	doc["version"] = float64(1)
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	var changes []string
	for _, sp := range specs {
		event := cursorEventName(sp.event)
		entry := map[string]any{
			"command": strings.Join(append([]string{shellQuote(exe)}, sp.args...), " "),
		}
		list, _ := hooks[event].([]any)
		if replaceMarkedEntry(&list, entry, sp.marker) {
			changes = append(changes, fmt.Sprintf("%s -> event %q", sp.name, event))
		}
		hooks[event] = list
	}
	doc["hooks"] = hooks

	if len(changes) == 0 {
		fmt.Fprintln(out, "Already up to date — no changes.")
		return nil
	}
	for _, c := range changes {
		fmt.Fprintf(out, "  + %s\n", c)
	}
	if dryRun {
		fmt.Fprintln(out, "\n--dry-run: not writing. Resulting hooks.json:")
		b, _ := json.MarshalIndent(doc, "", "  ")
		fmt.Fprintln(out, string(b))
		return nil
	}
	if err := writeSettings(path, doc); err != nil {
		return err
	}
	fmt.Fprintf(out, "Wrote %s (backup at %s.bak)\n", path, path)
	return nil
}

// cursorEventName maps our event names to Cursor's. Only the coaching
// nudge reaches here; read-guard is refused for Cursor before this point.
func cursorEventName(event string) string {
	switch {
	case strings.EqualFold(event, "Stop"):
		return "stop"
	case strings.EqualFold(event, "UserPromptSubmit"):
		// Cursor spells the prompt-submit hook differently from every
		// other client; writing the Claude Code name would produce a
		// file Cursor parses happily and never acts on.
		return "beforeSubmitPrompt"
	}
	return event
}

// replaceMarkedEntry adds or updates our own entry in a Cursor hook list,
// leaving anyone else's alone. Reports whether anything changed, so a
// re-run of `make install-hooks` does not rewrite a file it need not
// touch.
func replaceMarkedEntry(list *[]any, entry map[string]any, marker string) bool {
	for i, raw := range *list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := m["command"].(string)
		if !strings.Contains(cmd, marker) {
			continue
		}
		if cmd == entry["command"] {
			return false // already exactly right
		}
		(*list)[i] = entry
		return true
	}
	*list = append(*list, entry)
	return true
}
