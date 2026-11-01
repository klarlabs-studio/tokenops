package plans

import (
	"strings"
	"testing"
	"time"
)

// catalogTier is a tier as TierDrifts states it, from the catalog.
func catalogTier(t *testing.T, plan string, d time.Duration, windows int) DriftTier {
	t.Helper()
	_, f, ok := WindowFactor(plan, d)
	if !ok {
		t.Fatalf("%s has no factor in %s", plan, d)
	}
	p, _ := Lookup(plan)
	return DriftTier{Plan: p.Name, Display: p.Display, Windows: windows, Factor: f}
}

func drift(t *testing.T, low, high string, implied float64, drifted bool, windows int) TierDrift {
	t.Helper()
	lo, hi := catalogTier(t, low, week, windows), catalogTier(t, high, week, windows)
	return TierDrift{Provider: "anthropic", Window: "weekly", ModelFamily: "sonnet", Lower: lo, Higher: hi,
		CatalogRatio: hi.Factor / lo.Factor, ImpliedRatio: implied, Gap: implied/(hi.Factor/lo.Factor) - 1, Drifted: drifted}
}

// Only a drifted comparison is observed, the higher tier scaled to the
// measured ratio on the lower tier's catalog figure; a tier compared
// twice takes the comparison anchored lowest.
func TestObservedFromDrifts(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		drifts []TierDrift
		want   map[string]float64 // plan → observed factor
		anchor map[string]string
	}{
		{name: "none drifted", drifts: []TierDrift{drift(t, "claude-pro", "claude-max", 5.1, false, 6)}, want: map[string]float64{}},
		{name: "one drifted", drifts: []TierDrift{drift(t, "claude-pro", "claude-max", 3, true, 6)},
			want: map[string]float64{"claude-max": 3}, anchor: map[string]string{"claude-max": "claude-pro"}},
		{name: "anchored on the lowest tier", drifts: []TierDrift{
			drift(t, "claude-max", "claude-ultra", 2, true, 9),
			drift(t, "claude-pro", "claude-ultra", 12, true, 6),
		}, want: map[string]float64{"claude-ultra": 12}, anchor: map[string]string{"claude-ultra": "claude-pro"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ObservedFromDrifts(tc.drifts, now)
			if len(got) != len(tc.want) {
				t.Fatalf("observed = %+v", got)
			}
			for _, l := range got {
				if w, ok := tc.want[l.Plan]; !ok || l.ObservedFactor != w || l.Anchor != tc.anchor[l.Plan] || l.ObservedAt != now {
					t.Errorf("limit = %+v, want factor %v on %s", l, tc.want[l.Plan], tc.anchor[l.Plan])
				}
				if l.CatalogFactor == l.ObservedFactor || !strings.Contains(l.Words(), "catalog") || !strings.Contains(l.Words(), "observed") {
					t.Errorf("both figures not kept: %+v %q", l, l.Words())
				}
			}
		})
	}
}

// The overlay answers a tier's factor while it is on and the catalog
// still states the figures it was observed against; off, or after the
// catalog changed, it answers nothing. Replace swaps one provider's limits
// and puts none in while off.
func TestObservedLimitsOverlay(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	observed := ObservedFromDrifts([]TierDrift{drift(t, "claude-pro", "claude-max", 3, true, 6)}, now)
	stale := observed[0]
	stale.CatalogFactor, stale.CatalogRatio = 4, 4 // what the catalog said before a fix
	tests := []struct {
		name   string
		o      ObservedLimits
		plan   string
		d      time.Duration
		want   float64
		wantOK bool
	}{
		{name: "in force", o: ObservedLimits{Limits: observed}, plan: "claude-max", d: week, want: 3, wantOK: true},
		{name: "by an old name", o: ObservedLimits{Limits: observed}, plan: "claude-max-5x", d: week, want: 3, wantOK: true},
		{name: "another window", o: ObservedLimits{Limits: observed}, plan: "claude-max", d: 5 * time.Hour},
		{name: "another tier", o: ObservedLimits{Limits: observed}, plan: "claude-ultra", d: week},
		{name: "off", o: ObservedLimits{Off: true, Limits: observed}, plan: "claude-max", d: week},
		{name: "the catalog changed", o: ObservedLimits{Limits: []ObservedLimit{stale}}, plan: "claude-max", d: week},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, ok := tc.o.Factor(tc.plan, tc.d)
			if ok != tc.wantOK || (ok && l.ObservedFactor != tc.want) {
				t.Fatalf("factor = %+v, %v; want %v, %v", l, ok, tc.want, tc.wantOK)
			}
		})
	}

	o := ObservedLimits{Limits: append(observed, ObservedLimit{Provider: "openai", Plan: "x", Window: "weekly"})}
	o.Replace("anthropic", nil, now)
	if len(o.Limits) != 1 || o.Limits[0].Provider != "openai" || !o.UpdatedAt.Equal(now) {
		t.Errorf("after replacing anthropic with none: %+v", o)
	}
	o = ObservedLimits{Off: true}
	o.Replace("anthropic", observed, now)
	if len(o.Limits) != 0 {
		t.Errorf("an overlay that is off took limits: %+v", o)
	}
}
