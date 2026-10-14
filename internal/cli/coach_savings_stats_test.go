package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedSavings writes a savings ledger with a concluded compact-tip
// holdout (8 shown, 6 held back, $3.90 less spend after a shown tip) and the
// coach hook's session spend to match.
func seedSavings(t *testing.T, home string) {
	t.Helper()
	var ledger, events bytes.Buffer
	at := "2026-10-10T09:00:00Z"
	for i := range 8 {
		for _, c := range []struct {
			cohort string
			after  float64
		}{{"treated", 6 + float64(i)/10}, {"holdout", 10 + float64(i)/10}} {
			if c.cohort == "holdout" && i >= 6 {
				continue
			}
			sid := fmt.Sprintf("%s-%d", c.cohort, i)
			row, _ := json.Marshal(map[string]any{"type": "intervention", "id": sid, "at": at, "session": sid,
				"capability": "compaction", "kind": "compact_now", "cohort": c.cohort, "expected_usd": 0.31,
				"expected_basis": "per later turn, if acted on", "evidence": "context 700k", "session_usd": 12.0})
			ledger.Write(append(row, '\n'))
			ev, _ := json.Marshal(map[string]any{"ts": at, "session": sid, "cumulative_usd": 12 + c.after, "budget_usd": 50})
			events.Write(append(ev, '\n'))
		}
	}
	for path, body := range map[string][]byte{
		filepath.Join(home, ".tokenops", "coach", "savings.jsonl"):     ledger.Bytes(),
		filepath.Join(home, ".tokenops", "coach-hook", "events.jsonl"): events.Bytes(),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// coach stats shows the savings ledger: expected next to realized.
func TestCoachStatsShowsTheSavingsLedger(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	seedSavings(t, home)
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs([]string{"coach", "stats"})
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	t.Log(out.String())
	for _, want := range []string{"Savings ledger", "compact tips: 8 shown, 6 held back", "expected ~$0.31", "realized $3.90 per tip", "realized in total: $31.20"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("coach stats missing %q", want)
		}
	}
}
