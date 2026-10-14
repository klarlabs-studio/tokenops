package plans

import (
	"sort"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// MinCalibrationPeakPct is the least a window must have filled to measure
// its size: below it a single turn swings the ratio.
const MinCalibrationPeakPct = 5.0

// WindowCalibration is how many tokens one percent of a provider's plan
// window held, measured from the operator's own windows. It is an
// estimate: a vendor weighs tokens (cache reads, models) in ways it does
// not publish, and the ratio absorbs that only on average.
type WindowCalibration struct {
	Provider string `json:"provider"`
	// Window names the window kind ("weekly").
	Window string `json:"window"`
	// Windows is how many reset windows the ratio was measured over.
	Windows      int     `json:"windows"`
	TokensPerPct float64 `json:"tokens_per_pct"`
}

// Share is the percentage of one window that tokens are.
func (c WindowCalibration) Share(tokens int64) float64 {
	if c.TokensPerPct <= 0 {
		return 0
	}
	return float64(tokens) / c.TokensPerPct
}

// WindowSources are the readers whose events report plan windows, sorted.
func WindowSources() []string {
	out := make([]string, 0, len(windowSourceProvider))
	for s := range windowSourceProvider {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// WindowReadings collects every window reading in envs, by the provider
// whose plan it is.
func WindowReadings(envs []*eventschema.Envelope) map[string][]QuotaWindow {
	out := map[string][]QuotaWindow{}
	for _, e := range envs {
		if e == nil || e.Attributes == nil {
			continue
		}
		if p, ok := windowSourceProvider[e.Source]; ok {
			if ws := QuotaWindowsFromAttributes(p, e.Attributes); len(ws) > 0 {
				out[string(p)] = append(out[string(p)], ws...)
			}
		}
	}
	return out
}

// Calibrate measures tokens per percent of the provider's longest window
// kind over the windows that reset in [since, now]: for each, the tokens
// tokensIn counts while it was open against its peak; the median of
// those. False when no window filled to MinCalibrationPeakPct with tokens
// recorded in it.
func Calibrate(provider string, ws []QuotaWindow, since, now time.Time,
	tokensIn func(from, to time.Time) int64) (WindowCalibration, bool) {
	var measurable []WindowInstance
	for _, in := range WindowInstances(ws, since, now) {
		if in.Duration > 0 && in.PeakPct >= MinCalibrationPeakPct {
			measurable = append(measurable, in)
		}
	}
	// The longest kind; on a tie, the plainest name.
	var longest WindowInstance
	for _, in := range measurable {
		if in.Duration > longest.Duration || in.Duration == longest.Duration && (longest.Label == "" || len(in.Label) < len(longest.Label)) {
			longest = in
		}
	}
	var ratios []float64
	for _, in := range measurable {
		if in.Label != longest.Label {
			continue
		}
		if tokens := tokensIn(in.Start, in.End); tokens > 0 {
			ratios = append(ratios, float64(tokens)/in.PeakPct)
		}
	}
	if len(ratios) == 0 {
		return WindowCalibration{}, false
	}
	sort.Float64s(ratios)
	mid := ratios[len(ratios)/2]
	if len(ratios)%2 == 0 {
		mid = (ratios[len(ratios)/2-1] + mid) / 2
	}
	return WindowCalibration{Provider: provider, Window: longest.Label, Windows: len(ratios), TokensPerPct: mid}, true
}
