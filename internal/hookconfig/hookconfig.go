// Package hookconfig reads the TokenOps hook entries in a client's settings
// document. Claude Code nests a hook under its event, in groups that each
// name a matcher and hold the commands; Codex and Cursor write the whole
// invocation as one command string. Every reader of those files, the CLI's
// hook status and install, the coach's wiring and the boundary status, goes
// through this package so they agree on what is wired.
//
// The package does no I/O beyond Load, and imports nothing from TokenOps:
// callers decide which file to read and which events count.
package hookconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Location is one hook entry that runs a TokenOps verb.
type Location struct {
	// Event is the client's hook event (Stop, PreToolUse, ...).
	Event string
	// Matcher is the group's matcher, "" when the group has none.
	Matcher string
	// Command is the entry's command string.
	Command string
}

// Where narrows Has. An empty field matches any value.
type Where struct {
	Event   string
	Matcher string
}

// Parse decodes a settings document. An empty document is an empty object;
// malformed JSON is an error.
func Parse(b []byte) (map[string]any, error) {
	if len(strings.TrimSpace(string(b))) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// Load reads and parses the settings file at path.
func Load(path string) (map[string]any, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the operator's own client settings
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Hooks returns settings["hooks"] as a map, or an empty map when there is none.
func Hooks(settings map[string]any) map[string]any {
	if h, ok := settings["hooks"].(map[string]any); ok {
		return h
	}
	return map[string]any{}
}

// Find lists every entry, under any event, that runs verb.
func Find(hooks map[string]any, verb string) []Location {
	var out []Location
	for event, raw := range hooks {
		list, _ := raw.([]any)
		for _, elem := range list {
			_, matcher, entries := EventEntries(elem)
			for _, h := range entries {
				hm, ok := h.(map[string]any)
				if !ok || !IsEntry(hm, verb) {
					continue
				}
				cmd, _ := hm["command"].(string)
				out = append(out, Location{Event: event, Matcher: matcher, Command: cmd})
			}
		}
	}
	return out
}

// Has reports whether an entry that runs verb sits where w says.
func Has(hooks map[string]any, verb string, w Where) bool {
	for _, loc := range Find(hooks, verb) {
		if (w.Event == "" || loc.Event == w.Event) && (w.Matcher == "" || loc.Matcher == w.Matcher) {
			return true
		}
	}
	return false
}

// EventEntries returns the entries of one element of an event's list. A
// nested element is a group: it returns the group, its matcher and its hooks.
// A flat element is itself the entry (nil group, "" matcher), which is the
// shape some clients write.
func EventEntries(elem any) (group map[string]any, matcher string, entries []any) {
	m, ok := elem.(map[string]any)
	if !ok {
		return nil, "", nil
	}
	if hlist, nested := m["hooks"].([]any); nested {
		return m, MatcherOf(m), hlist
	}
	return nil, "", []any{m}
}

// MatcherOf returns a group's matcher, "" when it has none.
func MatcherOf(group map[string]any) string {
	s, _ := group["matcher"].(string)
	return s
}

// IsEntry reports whether one hook entry is a TokenOps entry running verb.
//
// Only a command-type entry counts. Claude Code takes `command` plus an
// `args` array, whose first element is the verb; the one-string shape
// carries the verb as the token after the binary.
func IsEntry(entry map[string]any, verb string) bool {
	if t, _ := entry["type"].(string); t != "" && t != "command" {
		return false
	}
	if args, _ := entry["args"].([]any); len(args) > 0 {
		first, _ := args[0].(string)
		return first == verb
	}
	cmd, _ := entry["command"].(string)
	return cmd != "" && CommandVerb(cmd) == verb
}

// CommandVerb returns the TokenOps subcommand in a one-string invocation,
// the token after the binary path.
//
// It cannot be strings.Fields(cmd)[1]: the writers quote a binary path
// holding spaces, so "'/Applications/My Tools/tokenops' route-guard" would
// report "Tools/tokenops'".
func CommandVerb(cmd string) string {
	fields := SplitCommand(cmd)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// SplitCommand splits on whitespace, treating a single-quoted run as one
// token. Single quotes are all the writers emit, so a full shell lexer would
// answer a question nothing asks.
func SplitCommand(s string) []string {
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
