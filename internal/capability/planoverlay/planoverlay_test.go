package planoverlay

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/checkup"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// sandbox keeps the overlay under a temporary home.
func sandbox(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, home)
	}
}

func drifts(t *testing.T) map[string][]plans.TierDrift {
	t.Helper()
	week := 7 * 24 * time.Hour
	_, lo, _ := plans.WindowFactor("claude-pro", week)
	_, hi, _ := plans.WindowFactor("claude-max", week)
	return map[string][]plans.TierDrift{"anthropic": {{
		Provider: "anthropic", Window: "weekly", ModelFamily: "sonnet", Drifted: true,
		Lower:        plans.DriftTier{Plan: "claude-pro", Display: "Pro", Windows: 5, Factor: lo},
		Higher:       plans.DriftTier{Plan: "claude-max", Display: "Max 5x", Windows: 6, Factor: hi},
		CatalogRatio: hi / lo, ImpliedRatio: 3,
	}}}
}

// Kept from drifted comparisons, the overlay shows both figures; reset
// goes back to the catalog and stays there through a keep; on observes
// again.
func TestOverlayLifecycle(t *testing.T) {
	sandbox(t)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	steps := []struct {
		name   string
		do     func() (View, error)
		off    bool
		limits int
	}{
		{"empty", Show, false, 0},
		{"nothing read", func() (View, error) { return Keep(nil, now) }, false, 0},
		{"kept", func() (View, error) { return Keep(drifts(t), now) }, false, 1},
		{"reset", func() (View, error) { return Reset(now) }, true, 0},
		{"kept while off", func() (View, error) { return Keep(drifts(t), now) }, true, 0},
		{"on without a store", func() (View, error) { return TurnOn(context.Background(), checkup.Options{Now: now}) }, false, 0},
		{"kept once on", func() (View, error) { return Keep(drifts(t), now) }, false, 1},
	}
	for _, s := range steps {
		v, err := s.do()
		if err != nil || v.Off != s.off || len(v.Limits) != s.limits || v.Limits == nil {
			t.Fatalf("%s: %+v, %v", s.name, v, err)
		}
		if s.limits > 0 && (v.Limits[0].ObservedFactor == v.Limits[0].CatalogFactor || v.Limits[0].Anchor != "claude-pro") {
			t.Errorf("%s: limit = %+v", s.name, v.Limits[0])
		}
		if Off() != s.off {
			t.Errorf("%s: Off() = %v", s.name, Off())
		}
	}
}
