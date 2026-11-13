package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.klarlabs.de/tokenops/internal/hookconfig"
	"go.klarlabs.de/tokenops/internal/infra/jsonfile"
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
	m, err := jsonfile.Parse(b)
	if err != nil {
		return nil, false, fmt.Errorf("parse settings %q: %w (refusing to overwrite)", path, err)
	}
	return m, true, nil
}

// hooksMap returns settings["hooks"] as a map, creating/normalising it.
func hooksMap(settings map[string]any) map[string]any {
	return hookconfig.Hooks(settings)
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

// retireMatchers removes the entries carrying marker from event's groups
// under any of matchers, pruning groups left empty. Returns whether
// anything was removed.
func retireMatchers(hooks map[string]any, event, marker string, matchers ...string) bool {
	if len(matchers) == 0 {
		return false
	}
	groups, _ := hooks[event].([]any)
	removed := false
	kept := make([]any, 0, len(groups))
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok || !slices.Contains(matchers, matcherOf(gm)) {
			kept = append(kept, g)
			continue
		}
		hlist, _ := gm["hooks"].([]any)
		rest := make([]any, 0, len(hlist))
		for _, h := range hlist {
			if hm, ok := h.(map[string]any); ok && isMarkerEntry(hm, marker) {
				removed = true
				continue
			}
			rest = append(rest, h)
		}
		if len(rest) == 0 {
			continue
		}
		gm["hooks"] = rest
		kept = append(kept, gm)
	}
	if removed {
		hooks[event] = kept
	}
	return removed
}

// eventEntries is hookconfig.EventEntries: a nested element is its group,
// its matcher and its hooks; a flat one is itself the entry.
func eventEntries(elem any) (group map[string]any, matcher string, entries []any) {
	return hookconfig.EventEntries(elem)
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

// findMarkerEntries lists the entries under every event that run marker.
func findMarkerEntries(hooks map[string]any, marker string) []markerLoc {
	found := hookconfig.Find(hooks, marker)
	out := make([]markerLoc, 0, len(found))
	for _, l := range found {
		out = append(out, markerLoc{event: l.Event, matcher: l.Matcher, command: l.Command})
	}
	return out
}

func matcherOf(group map[string]any) string {
	return hookconfig.MatcherOf(group)
}

func isMarkerEntry(entry map[string]any, marker string) bool {
	return hookconfig.IsEntry(entry, marker)
}

func commandVerb(cmd string) string {
	return hookconfig.CommandVerb(cmd)
}

func splitCommandLine(s string) []string {
	return hookconfig.SplitCommand(s)
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
	b, err := jsonfile.Edit(path, settings)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := jsonfile.WriteAtomic(path, b, mode); err != nil {
		return fmt.Errorf("write settings: %w", err)
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
