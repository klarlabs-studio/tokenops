package windowshare

import (
	"fmt"
	"sort"
	"time"
)

// MinSpreadSamples is the fewest measured tasks a spread is read from.
// Below it a 90th percentile is one task's luck.
const MinSpreadSamples = 5

// SpreadStatus says whether the spread could be read.
type SpreadStatus string

// Spread statuses.
const (
	SpreadMeasured SpreadStatus = "measured"
	SpreadTooFew   SpreadStatus = "too_few_samples"
)

// What a too-few spread falls back to.
const (
	// FallbackInterquartile: the middle-half estimate stands alone.
	FallbackInterquartile = "interquartile_estimate"
	// FallbackNone: there is no estimate either.
	FallbackNone = "none"
)

// Spread is how widely past runs of the same kind of task varied: the
// median and the 90th percentile of their measured share of the window,
// with the sample size. Runs of one task can differ by an order of
// magnitude in what they consume, so a single middle-half range hides the
// tail a plan has to leave room for.
//
// It is read only from measured outcomes (review_work's start and end
// readings of the plan's meter), never from an agent's own guess of what
// a task will take: those correlate poorly with what runs consume.
type Spread struct {
	Status     SpreadStatus `json:"status"`
	Basis      Basis        `json:"basis,omitempty"`
	Samples    int          `json:"samples"`
	MinSamples int          `json:"min_samples"`
	P50Pct     float64      `json:"p50_pct,omitempty"`
	P90Pct     float64      `json:"p90_pct,omitempty"`
	// Fallback names what the answer rests on when the spread is too
	// thin to read.
	Fallback string `json:"fallback,omitempty"`
	// Note says in a sentence what the spread is, or why there is none.
	Note string `json:"note"`
}

// Measured reports whether the spread carries percentiles.
func (s Spread) Measured() bool { return s.Status == SpreadMeasured }

// SpreadOf reads the spread from the same history Estimate reads, with
// the same preference: the kind of task in this scope when it has enough
// samples, else the kind anywhere. est is the estimate offered beside it,
// which a too-thin spread falls back to.
func SpreadOf(history []Sample, want Key, est EstimateResult) Spread {
	basis, shares, n := pick(history, want, MinSpreadSamples)
	if basis == "" {
		out := Spread{Status: SpreadTooFew, Samples: n, MinSamples: MinSpreadSamples, Fallback: FallbackNone}
		out.Note = fmt.Sprintf("only %d past tasks of this kind measured, too few for a p50/p90 (needs %d)", n, MinSpreadSamples)
		if est.Estimated() {
			out.Fallback = FallbackInterquartile
			out.Note += "; the range shown is the interquartile estimate alone"
		} else {
			out.Note += "; there is no estimate yet either"
		}
		return out
	}
	sort.Float64s(shares)
	out := Spread{
		Status: SpreadMeasured, Basis: basis, Samples: len(shares), MinSamples: MinSpreadSamples,
		P50Pct: round1(quantile(shares, 0.5)), P90Pct: round1(quantile(shares, 0.9)),
	}
	out.Note = fmt.Sprintf("over %d past tasks of this kind, half took up to %s%% of the window and nine in ten up to %s%%; plan for the p90",
		out.Samples, pct(out.P50Pct), pct(out.P90Pct))
	return out
}

// pick chooses the samples an estimate is read from: the kind in this
// scope when it has at least min, else the kind anywhere when that has.
// basis is empty when neither has enough; n is then the larger count.
func pick(history []Sample, want Key, min int) (basis Basis, shares []float64, n int) {
	var scoped, class []float64
	for _, s := range history {
		if s.Kind != want.Kind || s.Provider != want.Provider || s.Window != want.Window {
			continue
		}
		class = append(class, s.SharePct)
		if want.Scope != "" && s.Scope == want.Scope {
			scoped = append(scoped, s.SharePct)
		}
	}
	switch {
	case want.Scope != "" && len(scoped) >= min:
		return BasisClassAndScope, scoped, len(scoped)
	case len(class) >= min:
		return BasisClass, class, len(class)
	}
	return "", nil, len(class)
}

// SummaryWithSpread is Summary with the spread's p50, p90 and sample size
// after the range, when the spread could be read.
func SummaryWithSpread(est EstimateResult, sp Spread, now Reading, window time.Duration) string {
	if !est.Estimated() || !sp.Measured() {
		return Summary(est, now, window)
	}
	base := Summary(est, now, window)
	head := fmt.Sprintf("typically %s–%s%% of your %s window", pct(est.LowPct), pct(est.HighPct), WindowLabel(window))
	return head + fmt.Sprintf(" (p50 %s%%, p90 %s%%, n=%d)", pct(sp.P50Pct), pct(sp.P90Pct), sp.Samples) + base[len(head):]
}
