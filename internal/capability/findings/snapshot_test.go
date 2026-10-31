package findings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The ledgers hold every event since install. Read whole, July's 259 refused
// re-reads sat on the glance beside "Last 7 days" and read as this week's, while
// the team upload reported the week's 2. Only the window counts.
func TestLedgerStatsCountOnlyTheWindow(t *testing.T) {
	now := time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -WindowDays)
	old, recent := now.AddDate(0, 0, -100).Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339)

	guard := t.TempDir()
	writeLines(t, filepath.Join(guard, "events.jsonl"),
		`{"ts":"`+old+`","mode":"active","session":"a","path":"/x","action":"blocked","est_tokens":5000}`,
		`{"ts":"`+old+`","mode":"active","session":"a","path":"/x","action":"blocked","est_tokens":5000}`,
		`{"ts":"`+recent+`","mode":"active","session":"b","path":"/y","action":"blocked","est_tokens":700}`,
	)
	hook := t.TempDir()
	writeLines(t, filepath.Join(hook, "events.jsonl"),
		`{"ts":"`+old+`","session":"a","cumulative_usd":90,"budget_usd":10,"fraction":9,"tier_fired":1}`,
		`{"ts":"`+recent+`","session":"b","cumulative_usd":4,"budget_usd":10,"fraction":0.4,"tier_fired":0}`,
	)

	h, g := ledgerStats(hook, guard, since)
	if g == nil || g.Blocked != 1 || g.ReclaimedTok != 700 {
		t.Fatalf("read guard = %+v, want the one refusal in the window", g)
	}
	if h == nil || h.AlertsByTier["100%"] != 0 || h.DistinctSessions != 1 {
		t.Fatalf("coach hook = %+v, want one session and no over-budget alert in the window", h)
	}
	for _, f := range waste(g, nil) {
		if !strings.Contains(f.Title, "in the last 7 days") {
			t.Errorf("finding %q does not say its window", f.Title)
		}
	}
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
