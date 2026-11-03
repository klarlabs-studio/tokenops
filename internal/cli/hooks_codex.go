package cli

import (
	"fmt"
	"io"
	"strings"
)

// codexCommandEntry renders a hook as a single command string.
//
// Codex documents a command hook as `{"type":"command","command":"..."}`
// with no separate argument list. tokenops' Claude Code entries carry
// `args` alongside `command`, which Claude Code honours; assuming Codex
// does the same would, if wrong, run the bare binary with no subcommand —
// a hook that is installed, reports success and does nothing.
//
// title goes in statusMessage, which Codex's /hooks list titles the hook
// by ("Unnamed hook" without one) and shows while the hook runs.
func codexCommandEntry(exe string, args []string, title string) map[string]any {
	parts := append([]string{shellQuote(exe)}, args...)
	return map[string]any{
		"type":          "command",
		"command":       strings.Join(parts, " "),
		"timeout":       float64(10),
		"statusMessage": title,
	}
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
