package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// `tokenops coach` publishes each capability's bar for its next stage and,
// once the read guard's shadow evidence meets it, names the command —
// without changing anything.
func TestCoachShowsGraduation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	var ledger bytes.Buffer
	for i := range 55 {
		row, _ := json.Marshal(map[string]any{"type": "intervention", "id": fmt.Sprint("r", i), "at": "2026-10-10T09:00:00Z",
			"session": "s", "capability": "read_guard", "kind": "reread", "cohort": "shadow", "expected_tokens": 4000})
		ledger.Write(append(row, '\n'))
	}
	path := filepath.Join(home, ".tokenops", "coach", "savings.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, ledger.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("coach:\n  autonomy: advise\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := runCoachCmd(t)
	if err != nil {
		t.Fatal(err)
	}
	out := bytes.NewBufferString(got)
	t.Log(out.String())
	for _, want := range []string{
		"graduation (shadow → advise → act; you take each step)",
		"read guard      advise → act · ✓ shadow calls: 55 (needs at least 50)",
		"✓ realized saving: ~220k tokens",
		"ready: tokenops coach set waste autonomous",
		"compact tip",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("coach missing %q", want)
		}
	}
}
