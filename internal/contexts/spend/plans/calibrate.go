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
// whose plan it is, one per window at its peak (see WindowPeaks).
func WindowReadings(envs []*eventschema.Envelope) map[string][]QuotaWindow {
	var p WindowPeaks
	for _, e := range envs {
		p.Add(e)
	}
	return p.Readings()
}

// WindowPeaks collects window readings as they are read, keeping one per
// window: its kind, its length and when it resets, at the highest share
// read of it. A meter that reads every turn records the same window
// thousands of times; what WindowInstances and Calibrate take from those
// readings is each window's peak, so the peaks are all they need, and a
// long period's readings need not be held.
type WindowPeaks struct {
	peak map[string]map[windowKey]float64
}

// windowKey is one window: what WindowInstances groups readings by.
type windowKey struct {
	label    string
	resetsAt time.Time
	duration time.Duration
}

// Add takes in the windows e reports, when it is a window reading.
func (p *WindowPeaks) Add(e *eventschema.Envelope) {
	if e == nil || e.Attributes == nil {
		return
	}
	provider, ok := windowSourceProvider[e.Source]
	if !ok {
		return
	}
	for _, w := range QuotaWindowsFromAttributes(provider, e.Attributes) {
		if p.peak == nil {
			p.peak = map[string]map[windowKey]float64{}
		}
		byWindow := p.peak[string(provider)]
		if byWindow == nil {
			byWindow = map[windowKey]float64{}
			p.peak[string(provider)] = byWindow
		}
		k := windowKey{label: w.Label, resetsAt: w.ResetsAt, duration: w.Duration}
		if used, seen := byWindow[k]; !seen || w.UsedPct > used {
			byWindow[k] = w.UsedPct
		}
	}
}

// Readings are the windows taken in, by provider, each at its peak,
// ordered by when they reset, then by kind.
func (p *WindowPeaks) Readings() map[string][]QuotaWindow {
	out := map[string][]QuotaWindow{}
	for provider, byWindow := range p.peak {
		ws := make([]QuotaWindow, 0, len(byWindow))
		for k, used := range byWindow {
			ws = append(ws, QuotaWindow{Label: k.label, UsedPct: used, ResetsAt: k.resetsAt, Duration: k.duration})
		}
		sort.Slice(ws, func(i, j int) bool {
			if !ws[i].ResetsAt.Equal(ws[j].ResetsAt) {
				return ws[i].ResetsAt.Before(ws[j].ResetsAt)
			}
			if ws[i].Label != ws[j].Label {
				return ws[i].Label < ws[j].Label
			}
			return ws[i].Duration < ws[j].Duration
		})
		out[provider] = ws
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
