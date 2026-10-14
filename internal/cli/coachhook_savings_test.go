package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tip the hook gives is written to the realized-savings ledger with its
// decision ID, expected saving and evidence — figures only.
func TestCoachHookWritesTheSavingsLedger(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 60_000_000, "claude-opus-4-8")
	runCoach(t, dir, tp, "s-savings")
	b, err := os.ReadFile(filepath.Join(home, ".tokenops", "coach", "savings.jsonl"))
	if err != nil {
		t.Fatalf("no savings ledger: %v", err)
	}
	row := string(b)
	for _, want := range []string{`"type":"intervention"`, `"kind":"budget_50"`, `"capability":"session_budget"`, `"evidence":"`, `"session_usd":30`} {
		if !strings.Contains(row, want) {
			t.Errorf("row %s missing %s", row, want)
		}
	}
	if !strings.Contains(row, `"cohort":"treated"`) && !strings.Contains(row, `"cohort":"holdout"`) {
		t.Errorf("row has no cohort: %s", row)
	}
}
