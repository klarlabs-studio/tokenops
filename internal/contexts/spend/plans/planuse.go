package plans

import (
	"math"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Plan use is the commitment view of a flat-rate plan, after the cloud
// commitment KPIs: utilization is how much of the plan's windows was used
// (are the seats used?), coverage is how much of the AI usage the plan
// carried rather than metered overflow, gateways or router keys.

// PlanUseDays is the period the glance measures plan use over.
const PlanUseDays = 7

// WindowUse is how full one kind of plan window got over a period.
type WindowUse struct {
	// Label names the window kind ("5-hour", "weekly").
	Label string `json:"label"`
	// Windows is how many windows of the kind reset in the period.
	Windows int `json:"windows"`
	// AvgPeakPct is the mean of each window's highest reading.
	AvgPeakPct float64 `json:"avg_peak_pct"`
}

// PlanUse is a provider's plan utilization and coverage over a period.
// A figure that could not be measured is absent, never zero.
type PlanUse struct {
	Provider string `json:"provider"`
	// UtilizationPct is the mean peak of the longest plan window kind
	// that reset in the period, named by UtilizationWindow. The longest
	// kind runs whether or not the plan is used, so idle time counts
	// against it; a 5-hour window only exists once work starts.
	UtilizationPct    *float64 `json:"utilization_pct,omitempty"`
	UtilizationWindow string   `json:"utilization_window,omitempty"`
	// Windows is every window kind measured.
	Windows []WindowUse `json:"windows,omitempty"`
	// CoveragePct is the share of the provider's tokens a plan covered;
	// the rest was billed per token (an API key, a gateway, overflow).
	CoveragePct  *float64 `json:"coverage_pct,omitempty"`
	PlanTokens   int64    `json:"plan_tokens"`
	BilledTokens int64    `json:"billed_tokens"`
}

// PlanUseReport is plan use per provider over [Since, Until].
type PlanUseReport struct {
	Since     time.Time `json:"since"`
	Until     time.Time `json:"until"`
	Providers []PlanUse `json:"providers,omitempty"`
	// CoveragePct is the share of all providers' tokens a plan covered.
	CoveragePct *float64 `json:"coverage_pct,omitempty"`
}

// For returns provider's figures, if any.
func (r *PlanUseReport) For(provider string) (PlanUse, bool) {
	if r == nil {
		return PlanUse{}, false
	}
	for _, p := range r.Providers {
		if p.Provider == provider {
			return p, true
		}
	}
	return PlanUse{}, false
}

// windowSourceProvider maps each source that reports plan windows to the
// provider whose plan they are.
var windowSourceProvider = map[string]eventschema.Provider{
	"claude-usage-meter":     eventschema.ProviderAnthropic,
	"claude-code-statusline": eventschema.ProviderAnthropic,
	"claude-code-oauth":      eventschema.ProviderAnthropic,
	"codex-jsonl":            eventschema.ProviderOpenAI,
	"codex-app-server":       eventschema.ProviderOpenAI,
}

// PlanUseFrom measures plan use from the prompt events recorded in
// [since, now]: the window readings among them for utilization, the
// per-request turns for coverage.
func PlanUseFrom(envs []*eventschema.Envelope, since, now time.Time) PlanUseReport {
	return planUse(envs, since, since, now)
}

// PlanUseOn is plan use on one day, [start, end]: the windows that reset in
// it, read from every reading in envs however early (a weekly window
// filled up over the days before it reset), and the turns made in it.
func PlanUseOn(envs []*eventschema.Envelope, start, end time.Time) PlanUseReport {
	return planUse(envs, time.Time{}, start, end)
}

// planUse measures plan use in [since, now] from the window readings
// taken in [readFrom, now] and the turns made in [since, now].
func planUse(envs []*eventschema.Envelope, readFrom, since, now time.Time) PlanUseReport {
	readings := map[string][]QuotaWindow{}
	plan, billed := map[string]int64{}, map[string]int64{}
	for _, e := range envs {
		if e == nil || e.Timestamp.Before(readFrom) || e.Timestamp.After(now) {
			continue
		}
		if p, ok := windowSourceProvider[e.Source]; ok && e.Attributes != nil {
			readings[string(p)] = append(readings[string(p)], QuotaWindowsFromAttributes(p, e.Attributes)...)
		}
		if e.Timestamp.Before(since) {
			continue
		}
		pe, ok := e.Payload.(*eventschema.PromptEvent)
		if !ok || !perRequest(e) {
			continue
		}
		tokens := pe.TotalTokens
		if tokens == 0 {
			tokens = pe.InputTokens + pe.OutputTokens
		}
		if tokens <= 0 {
			continue
		}
		switch pe.CostSource {
		case eventschema.CostSourcePlanIncluded:
			plan[string(pe.Provider)] += tokens
		case eventschema.CostSourceTrial:
			// Free credits are neither a plan nor a bill.
		default:
			billed[string(pe.Provider)] += tokens
		}
	}
	providers := map[string]bool{}
	for p := range readings {
		providers[p] = true
	}
	for p := range plan {
		providers[p] = true
	}
	for p := range billed {
		providers[p] = true
	}
	out := PlanUseReport{Since: since, Until: now}
	var allPlan, allBilled int64
	for p := range providers {
		u := PlanUse{Provider: p, PlanTokens: plan[p], BilledTokens: billed[p]}
		u.Windows = windowUse(readings[p], since, now)
		if head, ok := headlineWindow(u.Windows, readings[p]); ok {
			pct := head.AvgPeakPct
			u.UtilizationPct, u.UtilizationWindow = &pct, head.Label
		}
		u.CoveragePct = share(plan[p], billed[p])
		allPlan += plan[p]
		allBilled += billed[p]
		if u.UtilizationPct == nil && u.CoveragePct == nil {
			continue
		}
		out.Providers = append(out.Providers, u)
	}
	sort.Slice(out.Providers, func(i, j int) bool { return out.Providers[i].Provider < out.Providers[j].Provider })
	out.CoveragePct = share(allPlan, allBilled)
	return out
}

func share(plan, billed int64) *float64 {
	if plan+billed <= 0 {
		return nil
	}
	pct := math.Round(float64(plan)/float64(plan+billed)*1000) / 10
	return &pct
}

// windowUse averages, per window kind, the peak of each instance that
// reset in the period.
func windowUse(ws []QuotaWindow, since, now time.Time) []WindowUse {
	type acc struct {
		sum float64
		n   int
	}
	byLabel := map[string]*acc{}
	for _, in := range WindowInstances(ws, since, now) {
		a := byLabel[in.Label]
		if a == nil {
			a = &acc{}
			byLabel[in.Label] = a
		}
		a.sum += in.PeakPct
		a.n++
	}
	out := make([]WindowUse, 0, len(byLabel))
	for label, a := range byLabel {
		out = append(out, WindowUse{Label: label, Windows: a.n, AvgPeakPct: math.Round(a.sum/float64(a.n)*10) / 10})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// WindowInstance is one window of a kind, from when it opened to its
// reset, and the highest share of it any reading saw.
type WindowInstance struct {
	Label    string
	Start    time.Time
	End      time.Time
	Duration time.Duration
	PeakPct  float64
}

// WindowInstances groups readings into window instances, one per kind and
// reset, and returns those that reset in [since, now], oldest first per
// kind. A window still open has not peaked yet, so it is left out, as are
// spend limits, which are money rather than a window.
func WindowInstances(ws []QuotaWindow, since, now time.Time) []WindowInstance {
	byLabel := map[string][]QuotaWindow{}
	for _, w := range ws {
		if w.Label == "" || w.ResetsAt.IsZero() || strings.Contains(w.Label, "spend limit") {
			continue
		}
		byLabel[w.Label] = append(byLabel[w.Label], w)
	}
	labels := make([]string, 0, len(byLabel))
	for label := range byLabel {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	var out []WindowInstance
	for _, label := range labels {
		rs := byLabel[label]
		sort.Slice(rs, func(i, j int) bool { return rs[i].ResetsAt.Before(rs[j].ResetsAt) })
		var cur *WindowInstance
		flush := func() {
			if cur != nil && !cur.End.Before(since) && !cur.End.After(now) {
				out = append(out, *cur)
			}
		}
		for _, r := range rs {
			if cur == nil || r.ResetsAt.Sub(cur.End) >= instanceResetGranularity {
				flush()
				cur = &WindowInstance{Label: label, End: r.ResetsAt, Duration: r.Duration, Start: r.ResetsAt.Add(-r.Duration)}
			}
			if r.UsedPct > cur.PeakPct {
				cur.PeakPct = math.Min(r.UsedPct, 100)
			}
		}
		flush()
	}
	return out
}

// headlineWindow is the longest window kind measured; on a tie, the one
// with the plainest name ("weekly" before "weekly Fable").
func headlineWindow(uses []WindowUse, ws []QuotaWindow) (WindowUse, bool) {
	length := map[string]time.Duration{}
	for _, w := range ws {
		length[w.Label] = w.Duration
	}
	var best WindowUse
	found := false
	for _, u := range uses {
		switch {
		case !found, length[u.Label] > length[best.Label],
			length[u.Label] == length[best.Label] && len(u.Label) < len(best.Label):
			best, found = u, true
		}
	}
	return best, found
}

// instanceResetGranularity absorbs the drift between two readings of the
// same window's reset time.
const instanceResetGranularity = 10 * time.Minute
