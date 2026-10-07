package opencodedb

import "testing"

func TestDefaultPath(t *testing.T) {
	t.Setenv("OPENCODE_DB", "/x/opencode.db")
	if p, _ := DefaultPath(); p != "/x/opencode.db" {
		t.Errorf("OPENCODE_DB: %q", p)
	}
	t.Setenv("OPENCODE_DB", "")
	t.Setenv("XDG_DATA_HOME", "/xdg")
	if p, _ := DefaultPath(); p != "/xdg/opencode/opencode.db" {
		t.Errorf("XDG: %q", p)
	}
}
