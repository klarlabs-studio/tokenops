package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

// shellQuote wraps a path holding spaces so the command string survives
// being split by a shell.
func shellQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"'") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
