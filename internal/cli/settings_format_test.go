package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Installing a hook rewrites the operator's settings.json without
// reordering their keys or escaping &, < and > in what was already there.
func TestWriteSettingsKeepsKeyOrderAndReadableCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	const original = "{\n  \"zeta\": true,\n  \"permissions\": {\"allow\": [\"Bash(a && b > c)\"]},\n  \"cleanupPeriodDays\": 633000\n}\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _, err := loadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	m["statusLine"] = map[string]any{"type": "command", "command": "tokenops statusline 2>/dev/null"}
	if err := writeSettings(path, m); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	got := string(b)
	if !strings.HasPrefix(got, original[:len(original)-3]+",\n  \"statusLine\": {") {
		t.Fatalf("untouched part changed:\n%s", got)
	}
	if !strings.Contains(got, "tokenops statusline 2>/dev/null") {
		t.Fatalf("new command escaped:\n%s", got)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}
