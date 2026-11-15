package boundaries

import (
	"maps"
	"slices"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readPlanFacts reads the plan's share left and how far ahead of its reset
// each window runs out on the one forecast, and at which rate: from Claude Code's status line
// in a Claude Code session, otherwise from the daemon's figures. A call
// that starts another agent reads that agent's plan too, and each window
// keeps the tighter reading.
func readPlanFacts(f *domain.Facts, call Call, env Env, figs *lazyFigures, now time.Time) ([]eventschema.EvidenceRef, []eventschema.WindowForecast) {
	var ev []eventschema.EvidenceRef
	var forecasts []eventschema.WindowForecast
	provider := call.Provider
	if call.Harness == HarnessClaudeCode {
		provider = "anthropic"
		if left, early, basis, observed, fc, ok := limitsPlan(env.LimitsPath, now); ok {
			f.PlanLeftPct, f.PlanEarlyMin, f.PlanEarlyBasis, f.PlanVendor = left, early, basis, planVendor(provider)
			forecasts = fc
			ev = append(ev, eventschema.EvidenceRef{
				Kind: "plan_headroom", Source: "claude_code_statusline", ObservedAt: observed,
				FreshUntil: observed.Add(limitsFreshFor), Confidence: 1, Scope: "anthropic",
			})
		}
	}
	if f.PlanLeftPct == nil {
		if fig, ok := figs.get(); ok {
			if left := figuresPlanLeft(fig, provider); len(left) > 0 {
				f.PlanEarlyMin, f.PlanEarlyBasis = figuresPlanEarly(fig, provider)
				forecasts = append(forecasts, figuresForecasts(fig, provider)...)
				f.PlanLeftPct, f.PlanVendor = left, planVendor(provider)
				scope, caveat := provider, ""
				if scope == "" {
					scope, caveat = "any", "the tightest plan TokenOps watches, whichever provider"
				}
				ev = append(ev, eventschema.EvidenceRef{
					Kind: "plan_headroom", Source: "daemon_glance", ObservedAt: fig.ObservedAt,
					FreshUntil: fig.ObservedAt.Add(figuresFreshFor), Confidence: 1, Scope: scope, Caveat: caveat,
				})
			}
		}
	}
	if len(call.Spends) == 0 {
		return ev, forecasts
	}
	fig, ok := figs.get()
	if !ok {
		return ev, forecasts
	}
	for _, other := range call.Spends {
		left := figuresPlanLeft(fig, other)
		if len(left) == 0 {
			continue
		}
		forecasts = append(forecasts, figuresForecasts(fig, other)...)
		vendor := planVendor(other)
		f.PlanLeftPct, f.PlanVendors, _ = mergePlan(f.PlanLeftPct, f.PlanVendors, f.PlanVendor, nil, left, vendor, nil, less)
		early, basis := figuresPlanEarly(fig, other)
		f.PlanEarlyMin, f.PlanEarlyVendors, f.PlanEarlyBasis = mergePlan(f.PlanEarlyMin, f.PlanEarlyVendors, f.PlanVendor,
			f.PlanEarlyBasis, early, vendor, basis, more)
		ev = append(ev, eventschema.EvidenceRef{
			Kind: "plan_headroom", Source: "daemon_glance", ObservedAt: fig.ObservedAt,
			FreshUntil: fig.ObservedAt.Add(figuresFreshFor), Confidence: 1, Scope: other,
			Caveat: "the tool call starts " + vendor + ", which spends that plan",
		})
	}
	return ev, forecasts
}

// figuresForecasts are the provider's windows as the daemon forecast
// them, or with no provider every plan's, in a stable order.
func figuresForecasts(fig boundaryledger.Figures, provider string) []eventschema.WindowForecast {
	var out []eventschema.WindowForecast
	for _, p := range slices.Sorted(maps.Keys(fig.PlanForecast)) {
		if provider != "" && p != provider {
			continue
		}
		byWindow := fig.PlanForecast[p]
		for _, w := range slices.Sorted(maps.Keys(byWindow)) {
			out = append(out, byWindow[w])
		}
	}
	return out
}

// windowForecast is one plan window as a pause or route records it: the
// share used, the pace the run-out is forecast at, and when it runs out
// at that pace (zero when it lasts to its reset). false for a window with
// no reset ahead.
func windowForecast(provider, name string, w plans.VendorWindow, now time.Time) (eventschema.WindowForecast, bool) {
	if name == "" || w.ResetsAt.IsZero() || !w.ResetsAt.After(now) {
		return eventschema.WindowForecast{}, false
	}
	f := eventschema.WindowForecast{Provider: provider, Window: name, UsedPct: w.UsedPct, ResetsAt: w.ResetsAt.UTC()}
	if w.UsedPct >= 100 {
		f.RunsOutAt = now.UTC()
		return f, true
	}
	pace := w.Forecast(now)
	if pace == nil || pace.TooEarly {
		return f, true
	}
	f.Basis = pace.Basis
	switch elapsed := w.Duration - w.ResetsAt.Sub(now); {
	case !pace.LastsToReset && pace.RunsOutIn > 0:
		f.RunsOutAt = now.Add(pace.RunsOutIn).UTC()
		f.PctPerHour = (100 - w.UsedPct) / pace.RunsOutIn.Hours()
	case elapsed > 0:
		f.PctPerHour = w.UsedPct / elapsed.Hours()
	}
	return f, true
}

func less(a, b float64) bool { return a < b }
func more(a, b float64) bool { return a > b }

// mergePlan keeps, for each window, the tighter of the session's plan
// reading and another plan's (the one tighter wins), whose each one is,
// and the forecast basis that goes with it.
func mergePlan(cur map[domain.Window]float64, vendors map[domain.Window]string, vendor string, curBasis map[domain.Window]string,
	other map[domain.Window]float64, otherVendor string, otherBasis map[domain.Window]string,
	tighter func(a, b float64) bool) (map[domain.Window]float64, map[domain.Window]string, map[domain.Window]string) {
	merged := maps.Clone(cur)
	if merged == nil {
		merged = map[domain.Window]float64{}
	}
	byWindow := map[domain.Window]string{}
	for w := range merged {
		byWindow[w] = vendor
		if v := vendors[w]; v != "" {
			byWindow[w] = v
		}
	}
	basis := maps.Clone(curBasis)
	if basis == nil {
		basis = map[domain.Window]string{}
	}
	for w, v := range other {
		if prev, ok := merged[w]; !ok || tighter(v, prev) {
			merged[w], byWindow[w] = v, otherVendor
			basis[w] = otherBasis[w]
			if basis[w] == "" {
				delete(basis, w)
			}
		}
	}
	return merged, byWindow, basis
}

// figuresPlanLeft is the provider's share left by window, or, with no
// provider, the tightest share each window has across every plan.
func figuresPlanLeft(fig boundaryledger.Figures, provider string) map[domain.Window]float64 {
	left, _ := figuresByWindow(fig.PlanLeftPct, provider, less)
	return left
}

// figuresPlanEarly is how many minutes ahead of its reset each of the
// provider's windows runs out, or, with no provider, the furthest ahead
// across every plan, and the rate each was forecast at.
func figuresPlanEarly(fig boundaryledger.Figures, provider string) (map[domain.Window]float64, map[domain.Window]string) {
	early, from := figuresByWindow(fig.PlanEarlyMin, provider, more)
	basis := map[domain.Window]string{}
	for w, p := range from {
		if b := fig.PlanEarlyBasis[p][string(w)]; b != "" {
			basis[w] = b
		}
	}
	return early, basis
}

// figuresByWindow is the tightest figure of each window, and the provider
// it came from.
func figuresByWindow(byProvider map[string]map[string]float64, provider string,
	tighter func(a, b float64) bool) (map[domain.Window]float64, map[domain.Window]string) {
	out, from := map[domain.Window]float64{}, map[domain.Window]string{}
	add := func(p string, windows map[string]float64) {
		for name, v := range windows {
			w := domain.Window(name)
			if w != domain.Window5h && w != domain.WindowWeek {
				continue
			}
			if cur, ok := out[w]; !ok || tighter(v, cur) {
				out[w], from[w] = v, p
			}
		}
	}
	if provider != "" {
		add(provider, byProvider[provider])
		return out, from
	}
	for p, windows := range byProvider {
		add(p, windows)
	}
	return out, from
}

// limitsPlan is the share of each Claude plan window left, how many
// minutes ahead of its reset it runs out on the one forecast, and the
// rate that forecast read, from the reading the status line keeps. A
// window that has reset since is unknown, not full: the reading no longer
// says anything about it.
func limitsPlan(path string, now time.Time) (left, early map[domain.Window]float64, basis map[domain.Window]string, observed time.Time,
	forecasts []eventschema.WindowForecast, ok bool) {
	r, found, err := claudelimits.Read(path)
	if err != nil || !found || now.Sub(r.ObservedAt) > limitsFreshFor {
		return nil, nil, nil, time.Time{}, nil, false
	}
	left, early, basis = map[domain.Window]float64{}, map[domain.Window]float64{}, map[domain.Window]string{}
	for w, win := range map[domain.Window]*claudelimits.Window{domain.Window5h: r.FiveHour, domain.WindowWeek: r.SevenDay} {
		if win == nil || (win.ResetsAt > 0 && time.Unix(win.ResetsAt, 0).Before(now)) {
			continue
		}
		left[w] = max(0, 100-win.UsedPct)
	}
	// How early each runs out is the one forecast glance shows: the last
	// hour's rate where the reading's history measures it.
	for _, vw := range r.Windows(now) {
		if _, kept := left[domain.Window(vw.Name)]; !kept {
			continue
		}
		if f, ok := windowForecast("anthropic", windowName(vw), vw, now); ok {
			forecasts = append(forecasts, f)
		}
		if before, known := vw.RunsOutEarly(now); known {
			early[domain.Window(vw.Name)] = before.Minutes()
			if f := vw.Forecast(now); f != nil && f.Basis != "" {
				basis[domain.Window(vw.Name)] = f.Basis
			}
		}
	}
	return left, early, basis, r.ObservedAt.UTC(), forecasts, len(left) > 0
}
