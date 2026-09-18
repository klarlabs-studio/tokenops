package cli

import (
	"strings"
	"testing"
)

// Six commands wrote config and then said "restart the daemon" without ever
// naming a command to do it with. There was no such command to name.
func TestRestartHintNamesARunnableCommand(t *testing.T) {
	hint := restartHint(true, false)
	if !strings.Contains(hint, "tokenops daemon restart") {
		t.Fatalf("a supervised daemon should be restartable by command: %q", hint)
	}
}

// Unsupervised, there is no unit to kickstart, so the honest instruction is
// the manual one — naming `tokenops daemon restart` would send the operator
// at a command that cannot work for them.
func TestRestartHintFallsBackWhenUnsupervised(t *testing.T) {
	hint := restartHint(false, false)
	if strings.Contains(hint, "tokenops daemon restart") {
		t.Fatalf("unsupervised has no unit to restart: %q", hint)
	}
	if !strings.Contains(hint, "tokenops start") {
		t.Fatalf("should name how to bring it back: %q", hint)
	}
}

// The MCP server's lifecycle belongs to the client that spawned it; tokenops
// cannot restart someone else's child process, so it says so plainly rather
// than offering a command that would not work.
func TestRestartHintAsksForTheClientWhenMCPIsAffected(t *testing.T) {
	hint := restartHint(true, true)
	if !strings.Contains(hint, "MCP") {
		t.Fatalf("want the MCP reload mentioned: %q", hint)
	}
	withoutMCP := restartHint(true, false)
	if strings.Contains(withoutMCP, "MCP") {
		t.Fatalf("provider/vendor changes do not need an MCP reload: %q", withoutMCP)
	}
}
