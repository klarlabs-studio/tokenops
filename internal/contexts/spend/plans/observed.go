package plans

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ObservedLimit is one tier's allowance in one kind of window as the
// operator's own window readings imply it, beside what the catalog says.
// It comes from a drifted tier comparison (TierDrifts) only: the same
// evidence thresholds as the catalog-drift finding.
//
// Readings measure the ratio between two tiers, not either tier's
// allowance, so the lower tier (Anchor) keeps its catalog figure and the
// higher tier's is scaled to the measured ratio. It is local to this
// machine and never written into the catalog: the catalog changes by pull
// request (ADR 0002), and an overlay whose catalog figures no longer
// match the catalog is dropped, since the catalog was changed.
type ObservedLimit struct {
	Provider string `json:"provider"`
	Plan     string `json:"plan"`
	Display  string `json:"display"`
	// Window is the window kind ("weekly", "5-hour").
	Window      string `json:"window"`
	ModelFamily string `json:"model_family"`
	// Anchor is the lower tier the measurement is relative to.
	Anchor        string `json:"anchor"`
	AnchorDisplay string `json:"anchor_display"`
	// CatalogFactor and ObservedFactor are Plan's allowance in Window as a
	// multiple of the family's base: the catalog's, and the readings'.
	CatalogFactor  float64 `json:"catalog_factor"`
	ObservedFactor float64 `json:"observed_factor"`
	// CatalogRatio and ObservedRatio are Plan's allowance over Anchor's.
	CatalogRatio  float64 `json:"catalog_ratio"`
	ObservedRatio float64 `json:"observed_ratio"`
	// Windows counts the window instances the comparison rests on, on
	// both tiers together.
	Windows    int       `json:"windows"`
	ObservedAt time.Time `json:"observed_at"`
}

// ObservedLimits is the local overlay of observed limits.
type ObservedLimits struct {
	// Off is the operator's choice to use the catalog only (`tokenops plan
	// observed reset`); nothing is observed into the overlay while it is.
	Off       bool            `json:"off,omitempty"`
	UpdatedAt time.Time       `json:"updated_at,omitzero"`
	Limits    []ObservedLimit `json:"limits,omitempty"`
}

// ObservedFromDrifts is the observed limits a provider's tier comparisons
// support: one per tier and window, from a drifted comparison only. A tier
// compared against several lower tiers takes the comparison anchored on
// the lowest, then the one with the most windows.
func ObservedFromDrifts(ds []TierDrift, now time.Time) []ObservedLimit {
	best := map[string]TierDrift{}
	for _, d := range ds {
		if !d.Drifted || d.Lower.Factor <= 0 || d.ImpliedRatio <= 0 {
			continue
		}
		k := d.Higher.Plan + "|" + d.Window
		cur, seen := best[k]
		if !seen || d.Lower.Factor < cur.Lower.Factor ||
			(d.Lower.Factor == cur.Lower.Factor && d.Lower.Windows+d.Higher.Windows > cur.Lower.Windows+cur.Higher.Windows) {
			best[k] = d
		}
	}
	out := make([]ObservedLimit, 0, len(best))
	for _, d := range best {
		out = append(out, ObservedLimit{
			Provider: d.Provider, Plan: d.Higher.Plan, Display: d.Higher.Display, Window: d.Window, ModelFamily: d.ModelFamily,
			Anchor: d.Lower.Plan, AnchorDisplay: d.Lower.Display,
			CatalogFactor: d.Higher.Factor, ObservedFactor: d.Lower.Factor * d.ImpliedRatio,
			CatalogRatio: d.CatalogRatio, ObservedRatio: d.ImpliedRatio,
			Windows: d.Lower.Windows + d.Higher.Windows, ObservedAt: now,
		})
	}
	sortObserved(out)
	return out
}

func sortObserved(ls []ObservedLimit) {
	sort.Slice(ls, func(i, j int) bool {
		a, b := ls[i], ls[j]
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		if a.Plan != b.Plan {
			return a.Plan < b.Plan
		}
		return a.Window < b.Window
	})
}

// Replace puts provider's observed limits in place of the ones it held:
// a comparison that no longer drifts takes its limit out. While the
// overlay is off, nothing is put in.
func (o *ObservedLimits) Replace(provider string, ls []ObservedLimit, now time.Time) {
	kept := o.Limits[:0:0]
	for _, l := range o.Limits {
		if l.Provider != provider {
			kept = append(kept, l)
		}
	}
	if !o.Off {
		kept = append(kept, ls...)
	}
	sortObserved(kept)
	o.Limits, o.UpdatedAt = kept, now
}

// Current is the observed limits still in force: none while the overlay
// is off, and none whose catalog figures the catalog no longer states.
func (o ObservedLimits) Current() []ObservedLimit {
	if o.Off {
		return nil
	}
	var out []ObservedLimit
	for _, l := range o.Limits {
		if l.matchesCatalog() {
			out = append(out, l)
		}
	}
	return out
}

// matchesCatalog reports whether the catalog still states the figures the
// limit was observed against.
func (l ObservedLimit) matchesCatalog() bool {
	d := labelDuration(l.Window)
	if d <= 0 {
		return false
	}
	_, f, ok := WindowFactor(l.Plan, d)
	_, fa, okA := WindowFactor(l.Anchor, d)
	if !ok || !okA || fa <= 0 {
		return false
	}
	return math.Abs(f-l.CatalogFactor) < 1e-9 && math.Abs(f/fa-l.CatalogRatio) < 1e-9
}

// Factor is the observed allowance of plan in a window of length d, when
// the overlay holds one in force.
func (o ObservedLimits) Factor(plan string, d time.Duration) (ObservedLimit, bool) {
	plan, label := canonicalPlan(plan), windowLabel(d)
	for _, l := range o.Current() {
		if l.Plan == plan && l.Window == label {
			return l, true
		}
	}
	return ObservedLimit{}, false
}

// Words says the limit with both figures: "Max 20x weekly: observed 7.0x
// Pro, catalog 20x (12 windows, Opus)".
func (l ObservedLimit) Words() string {
	return fmt.Sprintf("%s %s: observed %.1fx %s, catalog %gx (%d windows, %s)",
		l.Display, l.Window, l.ObservedRatio, l.AnchorDisplay, l.CatalogRatio, l.Windows, l.ModelFamily)
}

// labelDuration is the length of a window kind as windowLabel names it.
func labelDuration(label string) time.Duration {
	switch {
	case label == "weekly":
		return week
	case strings.HasSuffix(label, "-hour"):
		if h, err := strconv.Atoi(strings.TrimSuffix(label, "-hour")); err == nil && h > 0 {
			return time.Duration(h) * time.Hour
		}
	case strings.HasSuffix(label, "-day"):
		if n, err := strconv.Atoi(strings.TrimSuffix(label, "-day")); err == nil && n > 0 {
			return time.Duration(n) * 24 * time.Hour
		}
	}
	return 0
}
