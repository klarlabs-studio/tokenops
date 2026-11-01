package plans

import (
	"math"
	"strings"
	"testing"
	"time"
)

var priceNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// weekWindow is a weekly window used usedPct with resetsIn left.
func weekWindow(usedPct float64, resetsIn time.Duration) VendorWindow {
	return VendorWindow{Name: "week", UsedPct: usedPct, Duration: week, ResetsAt: priceNow.Add(resetsIn)}
}

// The price of quota is 0 while the window lasts to its reset at the pace
// so far, rises with how far the rest of it would overrun what is left,
// and is bounded: 1 once nothing is left.
func TestWindowPrice(t *testing.T) {
	for _, tc := range []struct {
		name       string
		w          VendorWindow
		lambda     float64
		basis      string
		wantedPct  float64 // 0 when the pace is not used
		runsOutPos bool
	}{
		// 40% used in 4 of 7 days: the last 3 days want 30%, 60% is left.
		{"slack", weekWindow(40, 3*24*time.Hour), 0, PriceBasisPace, 30, false},
		// 60% used in 4 days: the rest wants 45% against 40% left.
		{"slight overrun", weekWindow(60, 3*24*time.Hour), 1 - 40.0/45, PriceBasisPace, 45, true},
		// The maintainer's week: 46% used 30h into it wants ~212% more.
		{"heavy overrun", weekWindow(46, 5*24*time.Hour+18*time.Hour), 1 - 54/(46*138.0/30), PriceBasisPace, 46 * 138.0 / 30, true},
		{"nothing used yet", weekWindow(0, 3*24*time.Hour), 0, PriceBasisPace, 0, false},
		{"used up", weekWindow(100, 24*time.Hour), 1, PriceBasisUsedUp, 0, false},
		// Too early in the window to trust a pace: priced by share left,
		// 0 at a third left and above.
		{"early, plenty left", weekWindow(5, week-time.Hour), 0, PriceBasisShareLeft, 0, false},
		{"early, little left", weekWindow(80, week-time.Hour), 1 - 20/(100.0/3), PriceBasisShareLeft, 0, false},
		// No reset (the daemon's figures): share left alone.
		{"no reset, 40% left", VendorWindow{Name: "week", UsedPct: 60}, 0, PriceBasisShareLeft, 0, false},
		{"no reset, 20% left", VendorWindow{Name: "week", UsedPct: 80}, 0.4, PriceBasisShareLeft, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.w.Price(priceNow)
			if math.Abs(p.Lambda-tc.lambda) > 1e-9 {
				t.Errorf("λ = %v, want %v", p.Lambda, tc.lambda)
			}
			if p.Basis != tc.basis {
				t.Errorf("basis = %q, want %q", p.Basis, tc.basis)
			}
			if math.Abs(p.WantedPct-tc.wantedPct) > 1e-9 {
				t.Errorf("wanted = %v, want %v", p.WantedPct, tc.wantedPct)
			}
			if (p.RunsOutBeforeReset > 0) != tc.runsOutPos {
				t.Errorf("runs out before reset = %v", p.RunsOutBeforeReset)
			}
			if p.Lambda < 0 || p.Lambda > MaxQuotaPrice {
				t.Errorf("λ %v out of [0, %v]", p.Lambda, MaxQuotaPrice)
			}
		})
	}
}

// λ never falls as the pace quickens, and never leaves [0, 1].
func TestWindowPriceIsMonotoneAndBounded(t *testing.T) {
	prev := -1.0
	for used := 0.0; used <= 100; used += 2.5 {
		p := weekWindow(used, 3*24*time.Hour).Price(priceNow)
		if p.Lambda < prev {
			t.Fatalf("λ fell from %v to %v at %v%% used", prev, p.Lambda, used)
		}
		if p.Lambda < 0 || p.Lambda > MaxQuotaPrice {
			t.Fatalf("λ %v out of bounds at %v%% used", p.Lambda, used)
		}
		prev = p.Lambda
	}
	if prev != MaxQuotaPrice {
		t.Errorf("a used-up window costs %v, want %v", prev, MaxQuotaPrice)
	}
	// The same share used sooner is a faster pace and a higher price.
	prev = -1
	for resetsIn := 24 * time.Hour; resetsIn <= 6*24*time.Hour; resetsIn += 12 * time.Hour {
		p := weekWindow(50, resetsIn).Price(priceNow)
		if p.Lambda < prev {
			t.Fatalf("λ fell from %v to %v with %v left", prev, p.Lambda, resetsIn)
		}
		prev = p.Lambda
	}
}

// The binding window, the one with the highest price, sets the plan's.
func TestBindingPrice(t *testing.T) {
	fiveHour := VendorWindow{Name: "5h", UsedPct: 10, Duration: 5 * time.Hour, ResetsAt: priceNow.Add(4 * time.Hour)}
	p := BindingPrice("Claude", []VendorWindow{fiveHour, weekWindow(46, 5*24*time.Hour+18*time.Hour)}, priceNow)
	if p == nil || p.Window != "weekly" || p.Vendor != "Claude" || p.Lambda < 0.7 {
		t.Fatalf("binding = %+v", p)
	}
	// With slack everywhere the price is 0, and still says which window
	// is nearest to binding.
	p = BindingPrice("Claude", []VendorWindow{fiveHour, weekWindow(40, 3*24*time.Hour)}, priceNow)
	if p == nil || p.Lambda != 0 || p.Window != "weekly" {
		t.Fatalf("slack = %+v", p)
	}
	// Model-scoped, stale and already-reset windows say nothing.
	stale := weekWindow(99, 2*24*time.Hour)
	stale.Stale = true
	reset := weekWindow(99, -time.Minute)
	scoped := weekWindow(99, 2*24*time.Hour)
	scoped.Name = "week (Fable)"
	if p := BindingPrice("Claude", []VendorWindow{stale, reset, scoped}, priceNow); p != nil {
		t.Fatalf("unusable windows priced: %+v", p)
	}
}

func TestQuotaPriceWhy(t *testing.T) {
	p := BindingPrice("Claude", []VendorWindow{weekWindow(46, 5*24*time.Hour+18*time.Hour)}, priceNow)
	if got, want := p.Why(), "Claude weekly runs out ~4d 6h before it resets at this pace (54% left, ~212% wanted; quota price 0.74)"; got != want {
		t.Errorf("why = %q, want %q", got, want)
	}
	p = BindingPrice("Claude", []VendorWindow{{Name: "week", UsedPct: 80}}, priceNow)
	if got, want := p.Why(), "Claude weekly has 20% left (quota price 0.40)"; got != want {
		t.Errorf("why = %q, want %q", got, want)
	}
	p = BindingPrice("Claude", []VendorWindow{weekWindow(40, 3*24*time.Hour)}, priceNow)
	if got := p.Why(); !strings.Contains(got, "lasts to its reset") || !strings.Contains(got, "quota price 0") {
		t.Errorf("why = %q", got)
	}
	if !strings.HasPrefix(p.Evidence(), "quota_price: ") {
		t.Errorf("evidence = %q", p.Evidence())
	}
}
