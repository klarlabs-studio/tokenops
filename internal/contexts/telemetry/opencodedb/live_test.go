package opencodedb

import (
	"os"
	"testing"
)

// TestLiveStore reads a real opencode.db when OPENCODEDB_LIVE names one
// (a copy, never the live file opencode is writing). It checks the
// invariant the package exists for: every message once, whichever shape
// holds it.
func TestLiveStore(t *testing.T) {
	path := os.Getenv("OPENCODEDB_LIVE")
	if path == "" {
		t.Skip("set OPENCODEDB_LIVE to a copy of an opencode.db")
	}
	ids := map[string]int{}
	roles := map[Role]int{}
	tools := 0
	if err := Read(path, Options{Parts: true}, func(m Message) error {
		ids[m.ID]++
		roles[m.Role]++
		tools += len(m.Tools)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for id, n := range ids {
		if n > 1 {
			t.Fatalf("message %s read %d times", id, n)
		}
	}
	t.Logf("messages %d, roles %v, tool calls %d", len(ids), roles, tools)
}
