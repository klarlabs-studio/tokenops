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

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// wasteFixture is a period's waste report with every part filled.
func wasteFixture() avoidable.Report {
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	cls := func(c waste.Class, tokens int64, usd float64, guard string) avoidable.ClassCost {
		return avoidable.ClassCost{Class: c, Name: waste.ClassName(c), Guardrail: guard, Turns: 2, Sessions: 1, Method: "how " + string(c),
			Amount: avoidable.Amount{Tokens: tokens, APIEquivalentUSD: usd,
				WindowShare: []avoidable.WindowShare{{Provider: "anthropic", Window: "weekly", Pct: float64(tokens) / 1000}}}}
	}
	share := 0.25
	next := avoidable.Guardrail{Capability: learning.CapabilityReadGuard, Name: "read guard", Stage: learning.StageShadow,
		Next: learning.StageAdvise, Ready: true, Promote: "tokenops coach set waste advise", Amount: avoidable.Amount{APIEquivalentUSD: 3}}
	return avoidable.Report{
		Since: t0, Until: t0.Add(7 * 24 * time.Hour),
		Usage: avoidable.Amount{Tokens: 40_000, APIEquivalentUSD: 40,
			WindowShare: []avoidable.WindowShare{{Provider: "anthropic", Window: "weekly", Pct: 40}}},
		Avoidable: avoidable.Amount{Tokens: 12_000, APIEquivalentUSD: 12,
			WindowShare: []avoidable.WindowShare{{Provider: "anthropic", Window: "weekly", Pct: 12}}},
		Classes: []avoidable.ClassCost{
			cls(waste.ClassReread, 3_000, 3, learning.CapabilityReadGuard),
			cls(waste.ClassCacheRebuild, 0, 0, ""),
			cls(waste.ClassAbandoned, 9_000, 9, learning.CapabilitySessionBudget),
		},
		Realized: avoidable.Realized{HoldoutUSD: measurement.Unknown("no holdout comparison has concluded yet"),
			RefusedRereads: 2, RefusedTokens: 4_000, RefusedUSD: 4, USD: 4, Share: &share},
		Prevented: avoidable.Prevented{USD: 4, Mechanisms: []avoidable.Mechanism{{Key: avoidable.MechReadGuard,
			Name: "re-reads refused", Count: 2, Tokens: 4_000, Basis: "refused re-reads, at the session's price",
			USD: measurement.Estimated(4, "read guard ledger", "")}}},
		Guardrails: []avoidable.Guardrail{next, {Capability: learning.CapabilityRouting, Name: "model routing",
			Stage: learning.StageAdvise, Next: learning.StageAct, Bar: "✗ advice with an outcome: 3 (needs 50)"}},
		Next:         &next,
		Attribution:  avoidable.Attribution,
		Calibrations: []plans.WindowCalibration{{Provider: "anthropic", Window: "weekly", Windows: 3, TokensPerPct: 1000}},
		ShareMethod:  avoidable.ShareMethod,
		Notes:        []string{"no rate card: cache rebuilds and over-tiered turns are not priced"},
	}
}

// The classes a developer acts on carry their fix and where the dollars
// concentrate, beside the class's row.
func TestWriteWasteReportNamesFixAndConcentration(t *testing.T) {
	r := wasteFixture()
	r.Classes = append(r.Classes, avoidable.ClassCost{Class: waste.ClassIdleRebuild, Name: waste.ClassName(waste.ClassIdleRebuild),
		Amount: avoidable.Amount{Tokens: 500, APIEquivalentUSD: 0.5}, Turns: 1, Sessions: 9,
		Fix:           "keep a long session warm before the cache lapses",
		Concentration: &avoidable.Concentration{Sessions: 9, Top: 3, TopPct: 52}})
	var b bytes.Buffer
	writeWasteReport(&b, r)
	out := b.String()
	for _, want := range []string{
		"idle cache rebuilds",
		"3 sessions make 52% of it",
		"fix: keep a long session warm before the cache lapses",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestWriteWasteReport(t *testing.T) {
	r := wasteFixture()
	var b bytes.Buffer
	writeWasteReport(&b, r)
	out := b.String()
	for _, want := range []string{
		"Avoidable usage since",
		"used", "$40.00", "40.0% of the anthropic weekly window",
		"avoidable", "$12.00", "12.0%", "30% of usage",
		"redundant re-reads", "$3.00", "read guard",
		"abandoned sessions", "session budget",
		"~$4.00", "re-reads refused", "still avoidable",
		"Next guardrail: read guard", "`tokenops coach set waste advise`",
		"model routing", "✗ advice with an outcome",
		"at most one class", "how abandoned_session", "no rate card",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "cache-miss rebuilds  ") && strings.Contains(out, "how cache_rebuild") {
		t.Errorf("a class with nothing found is explained:\n%s", out)
	}
}

// `tokenops spend waste --json` runs end to end on an empty store.
func TestSpendWasteJSONOnAnEmptyStore(t *testing.T) {
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
	out, err := executeRoot(t, "spend", "waste", "--json", "--db", db, "--config", cfg, "--since", "7d")
	if err != nil {
		t.Fatalf("spend waste: %v", err)
	}
	var r avoidable.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("not the report: %v\n%s", err, out)
	}
	if r.Avoidable.Tokens != 0 || len(r.Classes) != len(waste.Classes()) || r.Attribution == "" {
		t.Errorf("report = %+v", r)
	}
	if len(r.Guardrails) == 0 {
		t.Error("the guardrails' graduation is read from the coach's configuration")
	}
}

// The cache rebuilds are split by cause under their class.
func TestWriteWasteReportSplitsRebuildsByCause(t *testing.T) {
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	share := func(pct float64) []avoidable.WindowShare {
		return []avoidable.WindowShare{{Provider: "anthropic", Window: "weekly", Pct: pct}}
	}
	r := avoidable.Report{Since: t0, Until: t0.Add(7 * 24 * time.Hour), Attribution: avoidable.Attribution,
		Classes: []avoidable.ClassCost{{Class: waste.ClassCacheRebuild, Name: waste.ClassName(waste.ClassCacheRebuild), Turns: 3,
			Amount: avoidable.Amount{Tokens: 3_000, APIEquivalentUSD: 3, WindowShare: share(3)},
			Causes: []avoidable.CauseCost{
				{Cause: waste.CauseTTLExpired, Name: waste.RebuildCauseName(waste.CauseTTLExpired), TTL: "1h", Turns: 2,
					Amount: avoidable.Amount{Tokens: 2_000, APIEquivalentUSD: 2, WindowShare: share(2)}},
				{Cause: waste.CauseModelSwitch, Name: waste.RebuildCauseName(waste.CauseModelSwitch), Turns: 1,
					Amount: avoidable.Amount{Tokens: 1_000, APIEquivalentUSD: 1, WindowShare: share(1)}},
			}}}}
	var b bytes.Buffer
	writeWasteReport(&b, r)
	out := b.String()
	for _, want := range []string{"    idle gaps over 1h  ", "2.0%", "model switches", "1.0%"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// A prompt prefix change names the context-lint item it was joined to,
// and says how the join is made; without one nothing is named.
func TestWriteWasteReportNamesTheLikelyLintFinding(t *testing.T) {
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	report := func(likely []avoidable.LikelyCause) string {
		prefix := avoidable.CauseCost{Cause: waste.CausePrefixChanged, Name: waste.RebuildCauseName(waste.CausePrefixChanged), Turns: 3,
			Amount: avoidable.Amount{Tokens: 3_000, APIEquivalentUSD: 3}, Likely: likely}
		if likely != nil {
			prefix.LikelyMethod = avoidable.LikelyMethod
		}
		r := avoidable.Report{Since: t0, Until: t0.Add(7 * 24 * time.Hour), Attribution: avoidable.Attribution,
			Classes: []avoidable.ClassCost{{Class: waste.ClassCacheRebuild, Name: waste.ClassName(waste.ClassCacheRebuild), Turns: 3,
				Amount: avoidable.Amount{Tokens: 3_000, APIEquivalentUSD: 3}, Causes: []avoidable.CauseCost{prefix}}}}
		var b bytes.Buffer
		writeWasteReport(&b, r)
		return b.String()
	}
	tests := []struct {
		name   string
		likely []avoidable.LikelyCause
		want   []string
		absent []string
	}{
		{name: "joined", likely: []avoidable.LikelyCause{{Finding: "SessionStart hook running `date`", Rebuilds: 3, Sessions: 2}},
			want: []string{"likely: SessionStart hook running `date` (3 of 3 rebuilds)", "likely: a rebuild is joined"}},
		{name: "none joined", absent: []string{"likely:"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := report(tt.likely)
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}
			for _, a := range tt.absent {
				if strings.Contains(out, a) {
					t.Errorf("unexpected %q in:\n%s", a, out)
				}
			}
		})
	}
}

// An inferred class is marked and said to be left out of the total.
func TestWriteWasteReportMarksTheInferredClass(t *testing.T) {
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	r := avoidable.Report{Since: t0, Until: t0.Add(7 * 24 * time.Hour), Attribution: avoidable.Attribution,
		Usage:     avoidable.Amount{Tokens: 10_000, APIEquivalentUSD: 10},
		Avoidable: avoidable.Amount{Tokens: 1_000, APIEquivalentUSD: 1},
		Classes: []avoidable.ClassCost{
			{Class: waste.ClassReread, Name: waste.ClassName(waste.ClassReread), Turns: 1, Amount: avoidable.Amount{Tokens: 1_000, APIEquivalentUSD: 1}},
			{Class: waste.ClassAbandoned, Name: waste.ClassName(waste.ClassAbandoned), Turns: 4, Inferred: true,
				Amount: avoidable.Amount{Tokens: 6_000, APIEquivalentUSD: 6}},
		}}
	var b bytes.Buffer
	writeWasteReport(&b, r)
	out := b.String()
	for _, want := range []string{"abandoned sessions *", "* inferred from no commit and no outcome", "not counted as avoidable", "10% of usage"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// Nothing inferred, no note.
	r.Classes = r.Classes[:1]
	b.Reset()
	writeWasteReport(&b, r)
	if strings.Contains(b.String(), "inferred") {
		t.Errorf("a note for no inferred class:\n%s", b.String())
	}
}

// Prevented sits above found, both labelled, and each mechanism says what
// it stopped and whether it is priced; the basis follows the classes.
func TestWriteWasteReportLeadsWithPreventedThenFound(t *testing.T) {
	r := wasteFixture()
	r.Prevented = avoidable.Prevented{USD: 4.5, Mechanisms: []avoidable.Mechanism{
		{Key: avoidable.MechReadGuard, Name: "re-reads refused", Count: 2, Tokens: 4_000, Basis: "basis of refusals",
			USD: measurement.Estimated(4, "read guard ledger", "")},
		{Key: avoidable.MechAutopilot, Name: "model moves verified by the holdout", Count: 6,
			USD: measurement.Estimated(0.5, "randomized holdout", "")},
		{Key: avoidable.MechBoundary, Name: "calls a boundary held", Count: 2,
			USD: measurement.Unknown("a held call is not priced")},
	}}
	var b bytes.Buffer
	writeWasteReport(&b, r)
	out := b.String()
	p, f := strings.Index(out, "prevented"), strings.Index(out, "found")
	if p < 0 || f < 0 || p > f {
		t.Fatalf("want prevented above found in:\n%s", out)
	}
	for _, want := range []string{"~$4.50", "re-reads refused", "not priced", "basis of refusals"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "realized") {
		t.Errorf("the old realized line is still printed:\n%s", out)
	}
}

// An avoided lockout reads as the minutes it would have blocked, marked an
// estimate, not as dollars.
func TestWritePreventedShowsBlockedMinutes(t *testing.T) {
	var b strings.Builder
	writePrevented(&b, avoidable.Prevented{Mechanisms: []avoidable.Mechanism{{
		Key: avoidable.MechLockout, Name: "plan lockouts avoided", Count: 1, BlockedMinutes: 90, Counterfactual: true,
		USD: measurement.Unknown("counterfactual"),
	}}})
	if !strings.Contains(b.String(), "~1.5h blocked (estimate)") {
		t.Errorf("prevented row:\n%s", b.String())
	}
}
