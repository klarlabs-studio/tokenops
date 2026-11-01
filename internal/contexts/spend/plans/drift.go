package plans

import (
	"math"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// The rule a tier ratio has to break before it is called drift. Each
// constant is a guard against reading noise as a vendor change.
const (
	// MinDriftWindows is the least windows each tier needs: fewer and one
	// unusual week decides the median.
	MinDriftWindows = 5
	// MinDriftPeakPct is the least a window must have filled: vendors
	// report whole percents, so a window at 4% carries a 12% rounding
	// error on its own.
	MinDriftPeakPct = 10.0
	// MinDriftFamilyShare is the share of a window's list value one model
	// family must hold for the window to count. A percent weighs models
	// differently, so a window that mixed them says nothing about the
	// tier until the vendor's weights are known, and it does not publish
	// them.
	MinDriftFamilyShare = 0.9
	// DriftThreshold is how far the measured ratio must sit from the
	// catalog's, as a fraction of the catalog's.
	DriftThreshold = 0.25
)

// DriftTurn is one plan-covered request, as a family and a list value.
type DriftTurn struct {
	At    time.Time
	Model string
	// ValueUSD is the request at API list price. Within one model family
	// it weighs output, input and cache reads as the vendor's own price
	// does, which raw tokens (mostly cache reads) do not.
	ValueUSD float64
}

// DriftInput is what TierDrifts reads for one provider.
type DriftInput struct {
	Provider string
	Readings []QuotaReading
	Turns    []DriftTurn
	// Since is the earliest instant turns were read from: a window that
	// opened before it has part of its use missing.
	Since time.Time
	// PlanAt is the catalog plan in force at an instant (plan history,
	// ADR 0008); empty when unknown.
	PlanAt func(time.Time) string
}

// DriftTier is one tier's side of a comparison.
type DriftTier struct {
	Plan    string `json:"plan"`
	Display string `json:"display"`
	Windows int    `json:"windows"`
	// Factor is the tier's allowance in this window as a multiple of the
	// family's base, as the catalog states it.
	Factor float64 `json:"factor"`
	// ValuePerPct is the median list value one percent of the window held
	// on this tier.
	ValuePerPct float64 `json:"value_per_pct"`
	q1, q3      float64
}

// TierDrift compares the ratio between two tiers the operator's own
// windows imply with the one the catalog states.
type TierDrift struct {
	Provider string `json:"provider"`
	// Window is the window kind ("weekly", "5-hour").
	Window string `json:"window"`
	// ModelFamily is the one family every window compared was spent on.
	ModelFamily string    `json:"model_family"`
	Lower       DriftTier `json:"lower"`
	Higher      DriftTier `json:"higher"`
	// CatalogRatio is Higher's allowance over Lower's in the catalog;
	// ImpliedRatio the same from the medians of the readings, and Low and
	// High the range their quartiles allow.
	CatalogRatio float64 `json:"catalog_ratio"`
	ImpliedRatio float64 `json:"implied_ratio"`
	Low          float64 `json:"low"`
	High         float64 `json:"high"`
	// Gap is ImpliedRatio over CatalogRatio, less one: -0.4 is 40% less
	// than the catalog says.
	Gap float64 `json:"gap"`
	// Drifted is a gap past DriftThreshold with the catalog's ratio
	// outside the quartile range: past the noise, not only past a line.
	Drifted bool `json:"drifted"`
}

// DriftTurnsFrom is every plan-covered request of provider in [since,
// until] priced at list, the way ListValue prices them. A request with no
// rate is left out: its family's share cannot be weighed.
func DriftTurnsFrom(envs []*eventschema.Envelope, provider string, since, until time.Time, price Pricer) []DriftTurn {
	var out []DriftTurn
	for _, e := range envs {
		if e == nil || e.Timestamp.Before(since) || e.Timestamp.After(until) || !perRequest(e) {
			continue
		}
		p, ok := e.Payload.(*eventschema.PromptEvent)
		if !ok || string(p.Provider) != provider || p.CostSource != eventschema.CostSourcePlanIncluded {
			continue
		}
		billed := *p
		billed.CostSource = eventschema.CostSourceMetered
		usd, err := price(&billed, e.Timestamp)
		if err != nil || usd <= 0 {
			continue
		}
		model := p.ResponseModel
		if model == "" {
			model = p.RequestModel
		}
		out = append(out, DriftTurn{At: e.Timestamp, Model: model, ValueUSD: usd})
	}
	return out
}

// TierDrifts compares every pair of tiers of one family the operator has
// enough windows on, per window kind and model family. It returns each
// comparison the evidence allows, drifted or not; none when the operator
// has used only one tier, since one tier alone has nothing to be checked
// against (the catalog's base is in chat messages, a unit the readings do
// not measure).
func TierDrifts(in DriftInput) []TierDrift {
	type key struct{ base, window, family string }
	samples := map[key]map[string][]float64{}
	turns := append([]DriftTurn(nil), in.Turns...)
	sort.Slice(turns, func(i, j int) bool { return turns[i].At.Before(turns[j].At) })
	for _, w := range driftWindows(in.Readings) {
		if w.start.Before(in.Since) || in.PlanAt == nil {
			continue
		}
		plan := canonicalPlan(in.PlanAt(w.start))
		if plan == "" || plan != canonicalPlan(in.PlanAt(w.peakAt)) {
			continue
		}
		base, _, ok := WindowFactor(plan, w.duration)
		if !ok {
			continue
		}
		family, value, ok := dominantFamily(turns, w.start, w.peakAt)
		if !ok {
			continue
		}
		k := key{base, w.label, family}
		if samples[k] == nil {
			samples[k] = map[string][]float64{}
		}
		samples[k][plan] = append(samples[k][plan], value/w.peakPct)
	}
	var out []TierDrift
	for k, byPlan := range samples {
		var tiers []DriftTier
		for plan, vs := range byPlan {
			if len(vs) < MinDriftWindows {
				continue
			}
			_, factor, _ := WindowFactor(plan, durationOf(k.window, in.Readings))
			sort.Float64s(vs)
			p, _ := Lookup(plan)
			tiers = append(tiers, DriftTier{Plan: plan, Display: p.Display, Windows: len(vs), Factor: factor,
				ValuePerPct: quantile(vs, 0.5), q1: quantile(vs, 0.25), q3: quantile(vs, 0.75)})
		}
		sort.Slice(tiers, func(i, j int) bool {
			if tiers[i].Factor != tiers[j].Factor {
				return tiers[i].Factor < tiers[j].Factor
			}
			return tiers[i].Plan < tiers[j].Plan
		})
		for i := range tiers {
			for j := i + 1; j < len(tiers); j++ {
				out = append(out, compareTiers(in.Provider, k.window, k.family, tiers[i], tiers[j]))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Window != b.Window {
			return a.Window < b.Window
		}
		if a.ModelFamily != b.ModelFamily {
			return a.ModelFamily < b.ModelFamily
		}
		return a.Higher.Plan < b.Higher.Plan
	})
	return out
}

// compareTiers applies the drift rule to one pair.
func compareTiers(provider, window, family string, lo, hi DriftTier) TierDrift {
	d := TierDrift{Provider: provider, Window: window, ModelFamily: family, Lower: lo, Higher: hi,
		CatalogRatio: hi.Factor / lo.Factor, ImpliedRatio: hi.ValuePerPct / lo.ValuePerPct}
	if lo.q3 > 0 {
		d.Low = hi.q1 / lo.q3
	}
	if lo.q1 > 0 {
		d.High = hi.q3 / lo.q1
	} else {
		d.High = math.Inf(1)
	}
	d.Gap = d.ImpliedRatio/d.CatalogRatio - 1
	d.Drifted = math.Abs(d.Gap) >= DriftThreshold && (d.CatalogRatio < d.Low || d.CatalogRatio > d.High)
	return d
}

// canonicalPlan is the catalog's current name for a plan, so a window on
// "claude-max-5x" and one on "claude-max" are the same tier.
func canonicalPlan(name string) string {
	if name == "" {
		return ""
	}
	if p, ok := Lookup(name); ok {
		return p.Name
	}
	return name
}

// durationOf is the length readings give a window kind.
func durationOf(label string, rs []QuotaReading) time.Duration {
	for _, r := range rs {
		for _, w := range r.Windows {
			if w.Label == label && w.Duration > 0 {
				return w.Duration
			}
		}
	}
	return 0
}

// driftWindow is one window instance with the reading that saw it fullest.
type driftWindow struct {
	label    string
	duration time.Duration
	start    time.Time
	reset    time.Time
	peakPct  float64
	peakAt   time.Time
}

// driftWindows groups readings into window instances, one per kind and
// reset, and keeps those that filled enough to measure without reaching
// the limit: a full window cannot say how far past it demand went.
func driftWindows(rs []QuotaReading) []driftWindow {
	byKey := map[string][]*driftWindow{}
	var all []*driftWindow
	for _, r := range rs {
		for _, w := range r.Windows {
			if w.Label == "" || w.ResetsAt.IsZero() || w.Duration <= 0 || strings.Contains(w.Label, "spend limit") {
				continue
			}
			var cur *driftWindow
			for _, c := range byKey[w.Label] {
				if absDuration(c.reset.Sub(w.ResetsAt)) < instanceResetGranularity {
					cur = c
					break
				}
			}
			if cur == nil {
				cur = &driftWindow{label: w.Label, duration: w.Duration, reset: w.ResetsAt, start: w.ResetsAt.Add(-w.Duration)}
				byKey[w.Label] = append(byKey[w.Label], cur)
				all = append(all, cur)
			}
			if w.UsedPct > cur.peakPct {
				cur.peakPct, cur.peakAt = w.UsedPct, r.At
			}
		}
	}
	var out []driftWindow
	for _, w := range all {
		if w.peakPct >= MinDriftPeakPct && w.peakPct < 100 {
			out = append(out, *w)
		}
	}
	return out
}

// dominantFamily is the model family that holds at least
// MinDriftFamilyShare of the list value spent in [from, to], with all the
// value spent: the rest filled the window too. turns are sorted by time.
func dominantFamily(turns []DriftTurn, from, to time.Time) (string, float64, bool) {
	i := sort.Search(len(turns), func(i int) bool { return !turns[i].At.Before(from) })
	by := map[string]float64{}
	var total float64
	for ; i < len(turns) && !turns[i].At.After(to); i++ {
		f := modelFamily(turns[i].Model)
		by[f] += turns[i].ValueUSD
		total += turns[i].ValueUSD
	}
	if total <= 0 {
		return "", 0, false
	}
	for f, v := range by {
		if v/total >= MinDriftFamilyShare {
			return f, total, true
		}
	}
	return "", 0, false
}

// familyNames are the model families a vendor's limits are known to weigh
// apart. A model in none of them is its own family: the strictest control.
var familyNames = []string{"opus", "sonnet", "haiku", "fable"}

// modelFamily names the family a model belongs to.
func modelFamily(model string) string {
	m := strings.ToLower(model)
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	for _, f := range familyNames {
		if strings.Contains(m, f) {
			return f
		}
	}
	return m
}

// quantile is the p-quantile of sorted values, interpolated.
func quantile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}
