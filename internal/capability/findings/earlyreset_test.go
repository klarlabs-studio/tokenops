package findings

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// The Codex weekly window that reset 5d 12h early: 62% used at 09:00 on
// the 25th, due to reset at 22:00 on the 30th, and 0% at 10:00 that day.
func codexEarlyReset() plans.EarlyReset {
	return plans.EarlyReset{
		Provider:       "openai",
		Name:           "week",
		Duration:       7 * 24 * time.Hour,
		PrevUsedPct:    62,
		PrevResetsAt:   time.Date(2026, 10, 30, 22, 0, 0, 0, time.UTC),
		PrevObservedAt: time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC),
		UsedPct:        0,
		ObservedAt:     time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC),
		ResetsAt:       time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC),
	}
}

func TestEarlyResetIsAnInfoFinding(t *testing.T) {
	r := Compute(Inputs{Glance: &headroom.Glance{EarlyResets: []plans.EarlyReset{codexEarlyReset()}}})
	if len(r.Findings) != 1 {
		t.Fatalf("findings %+v", r.Findings)
	}
	f := r.Findings[0]
	if f.Kind != KindPlan || f.Level != LevelInfo {
		t.Errorf("kind %s level %s", f.Kind, f.Level)
	}
	if want := "Codex's weekly window reset 5d 12h early"; f.Title != want {
		t.Errorf("title %q, want %q", f.Title, want)
	}
	for _, part := range []string{"62% used", "0% used"} {
		if !strings.Contains(f.Evidence, part) {
			t.Errorf("evidence %q lacks %q", f.Evidence, part)
		}
	}
}

func TestEarlyResetNamesTheVendorFromTheProvider(t *testing.T) {
	early := codexEarlyReset()
	early.Provider, early.Name = "anthropic", "5h"
	early.Duration = 5 * time.Hour
	early.PrevResetsAt = early.ObservedAt.Add(7 * time.Hour)
	r := Compute(Inputs{Glance: &headroom.Glance{EarlyResets: []plans.EarlyReset{early}}})
	if len(r.Findings) != 1 || !strings.HasPrefix(r.Findings[0].Title, "Claude's session window reset") {
		t.Errorf("findings %+v", r.Findings)
	}
}

func TestNoEarlyResetNoFinding(t *testing.T) {
	r := Compute(Inputs{Glance: &headroom.Glance{}})
	if len(r.Findings) != 0 {
		t.Errorf("findings %+v", r.Findings)
	}
}
