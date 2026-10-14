package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/coachhook"
)

// coach stats shows where the session budget came from, and when a
// clamp held it back from what the history said.
func TestWriteCoachHookStats_SessionBudgetProvenance(t *testing.T) {
	b := coachhook.SessionBudget{
		BudgetUSD: 45, Source: "history", Percentile: 0.9, ObservedUSD: 72,
		Clamp: "drift_up", PreviousUSD: 36, MaxDrift: 0.25, Sessions: 113, WindowDays: 30,
		DerivedAt: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC),
	}
	var out bytes.Buffer
	writeCoachHookStats(&out, coachhook.Stats{Events: 1, DistinctSessions: 1, AlertsByTier: map[string]int{}, SessionBudget: &b})
	got := out.String()
	for _, want := range []string{
		"session budget: $45 — your p90 session, from 113 sessions in 30 days (derived 2026-10-14)",
		"held from $72: moves at most 25% a week (was $36)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
