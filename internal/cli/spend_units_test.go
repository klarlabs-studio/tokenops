package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

func TestWriteUnitReport(t *testing.T) {
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	turn := func(at time.Duration, usd float64) analytics.SessionTurn {
		return analytics.SessionTurn{SessionID: "a", At: t0.Add(at), Provider: "anthropic", Tokens: 1000, APIEquivalentUSD: usd, Priced: true}
	}
	r := uniteconomics.Build(uniteconomics.Inputs{
		Since: t0, Until: t0.Add(7 * 24 * time.Hour),
		Dirs:   map[string]sessiondirs.Dir{"a": {CWD: "/w/app"}},
		RootOf: map[string]string{"/w/app": "/w/app"},
		CommitsOf: map[string][]gitlog.Commit{"/w/app": {
			{SHA: "0000000000", At: t0.Add(-10*24*time.Hour + time.Minute), Subject: "fix: old"},
			{SHA: "1111111111", At: t0.Add(time.Hour), Subject: "fix: new"},
			{SHA: "2222222222", At: t0.Add(48 * time.Hour), Subject: `Revert "fix: new"`, Reverts: "1111111111"},
		}},
		PRsOf: map[string][]gitlog.PR{"/w/app": {{Number: 2, MergedAt: t0.Add(2 * time.Hour)}}},
		Turns: []analytics.SessionTurn{turn(-10*24*time.Hour, 10), turn(0, 5), turn(3*time.Hour, 1)},
	})
	var b bytes.Buffer
	writeUnitReport(&b, r)
	out := b.String()
	for _, want := range []string{
		"Unit costs since", "PER UNIT", "4-WEEK BASELINE",
		"commit                 $5.00      1   $10.00 (1)          -50%",
		"merged PR              $5.00      1",
		"recorded outcome           —      0",
		"By change class, per commit", "bugfix",
		"no pull request merged yet $1.00",
		"How work was joined", "merged PR: time window",
		"Work that did not stand: 83% of the period", "reverted within 14 days  $5.00 on 1 commit",
		"did not stand: work placed on a commit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "fix: new") || strings.Contains(out, "/w/app") {
		t.Errorf("the report prints a subject or path:\n%s", out)
	}
}

// `tokenops spend units --json` runs end to end on an empty store: no
// units, every figure absent rather than zero.
func TestSpendUnitsJSONOnAnEmptyStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfg, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(home, "events.db")
	store, err := sqlite.Open(context.Background(), db, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	out, err := executeRoot(t, "spend", "units", "--json", "--db", db, "--config", cfg, "--since", "7d")
	if err != nil {
		t.Fatalf("spend units: %v", err)
	}
	var r uniteconomics.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("not the report: %v\n%s", err, out)
	}
	if r.PRs.Current.Units != 0 || r.PRs.Current.PerUnitUSD != nil || r.BaselineSince.IsZero() {
		t.Errorf("report = %+v", r)
	}
}
