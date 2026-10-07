package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

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
