package planhistory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A history recorded before Anthropic renamed Max 20x to Ultra reads, and
// is rewritten, under the current names; a line that does not parse and
// every field but the plan stay as written.
func TestRenameRetiredPlans(t *testing.T) {
	f := File{Path: filepath.Join(t.TempDir(), "plan-history.jsonl")}
	before := `{"provider":"anthropic","plan":"claude-max-5x","from":"2026-08-01T00:00:00Z","recorded":"2026-08-01T00:00:00Z"}
not json
{"provider":"anthropic","plan":"claude-max-20x","from":"2026-09-01T00:00:00Z","recorded":"2026-09-01T00:00:00Z","price":214.6,"currency":"EUR","note":"kept"}
{"provider":"openai","plan":"gpt-pro-5x","from":"2026-09-15T00:00:00Z","recorded":"2026-09-15T00:00:00Z"}
`
	if err := os.WriteFile(f.Path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := f.Load()
	if err != nil || len(h) != 3 || h[0].Plan != "claude-max" || h[1].Plan != "claude-ultra" || h[1].Price != 214.6 {
		t.Fatalf("Load = %+v, %v", h, err)
	}
	if b, _ := os.ReadFile(f.Path); string(b) != before {
		t.Fatal("reading rewrote the history")
	}

	n, err := f.RenameRetiredPlans()
	if err != nil || n != 2 {
		t.Fatalf("renamed %d, %v; want 2", n, err)
	}
	b, err := os.ReadFile(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 4 || lines[1] != "not json" ||
		!strings.Contains(lines[0], `"plan":"claude-max"`) ||
		!strings.Contains(lines[2], `"plan":"claude-ultra"`) || !strings.Contains(lines[2], `"note":"kept"`) ||
		!strings.Contains(lines[2], `"currency":"EUR"`) || lines[3] != strings.Split(before, "\n")[3] {
		t.Errorf("history =\n%s", b)
	}
	if n, err := f.RenameRetiredPlans(); err != nil || n != 0 {
		t.Errorf("second run renamed %d, %v", n, err)
	}
	if n, err := (File{Path: filepath.Join(t.TempDir(), "absent.jsonl")}).RenameRetiredPlans(); err != nil || n != 0 {
		t.Errorf("missing file renamed %d, %v", n, err)
	}
}
