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

func TestWriteWasteReport(t *testing.T) {
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	cls := func(c waste.Class, tokens int64, usd float64, guard string) avoidable.ClassCost {
		return avoidable.ClassCost{Class: c, Name: waste.ClassName(c), Guardrail: guard, Turns: 2, Sessions: 1, Method: "how " + string(c),
			Amount: avoidable.Amount{Tokens: tokens, APIEquivalentUSD: usd,
				WindowShare: []avoidable.WindowShare{{Provider: "anthropic", Window: "weekly", Pct: float64(tokens) / 1000}}}}
	}
	share := 0.25
	next := avoidable.Guardrail{Capability: learning.CapabilityReadGuard, Name: "read guard", Stage: learning.StageShadow,
		Next: learning.StageAdvise, Ready: true, Promote: "tokenops coach set waste advise", Amount: avoidable.Amount{APIEquivalentUSD: 3}}
	r := avoidable.Report{
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
		Guardrails: []avoidable.Guardrail{next, {Capability: learning.CapabilityRouting, Name: "model routing",
			Stage: learning.StageAdvise, Next: learning.StageAct, Bar: "✗ advice with an outcome: 3 (needs 50)"}},
		Next:         &next,
		Attribution:  avoidable.Attribution,
		Calibrations: []plans.WindowCalibration{{Provider: "anthropic", Window: "weekly", Windows: 3, TokensPerPct: 1000}},
		ShareMethod:  avoidable.ShareMethod,
		Notes:        []string{"no rate card: cache rebuilds and over-tiered turns are not priced"},
	}
	var b bytes.Buffer
	writeWasteReport(&b, r)
	out := b.String()
	for _, want := range []string{
		"Avoidable usage since",
		"used", "$40.00", "40.0% of the anthropic weekly window",
		"avoidable", "$12.00", "12.0%", "30% of usage",
		"redundant re-reads", "$3.00", "read guard",
		"abandoned sessions", "session budget",
		"Realized $4.00", "2 re-reads refused", "25% of what could be saved",
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
