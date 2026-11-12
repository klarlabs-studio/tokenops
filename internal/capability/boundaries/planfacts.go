package boundaries

import (
	"maps"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readPlanFacts reads the plan's share left and how far ahead of its reset
// each window runs out at the pace so far: from Claude Code's status line
// in a Claude Code session, otherwise from the daemon's figures. A call
// that starts another agent reads that agent's plan too, and each window
// keeps the tighter reading.
func readPlanFacts(f *domain.Facts, call Call, env Env, figs *lazyFigures, now time.Time) []eventschema.EvidenceRef {
	var ev []eventschema.EvidenceRef
	provider := call.Provider
	if call.Harness == HarnessClaudeCode {
		provider = "anthropic"
		if left, early, observed, ok := limitsPlan(env.LimitsPath, now); ok {
			f.PlanLeftPct, f.PlanEarlyMin, f.PlanVendor = left, early, planVendor(provider)
			ev = append(ev, eventschema.EvidenceRef{
				Kind: "plan_headroom", Source: "claude_code_statusline", ObservedAt: observed,
				FreshUntil: observed.Add(limitsFreshFor), Confidence: 1, Scope: "anthropic",
			})
		}
	}
	if f.PlanLeftPct == nil {
		if fig, ok := figs.get(); ok {
			if left := figuresPlanLeft(fig, provider); len(left) > 0 {
				f.PlanLeftPct, f.PlanEarlyMin, f.PlanVendor = left, figuresPlanEarly(fig, provider), planVendor(provider)
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
		return ev
	}
	fig, ok := figs.get()
	if !ok {
		return ev
	}
	for _, other := range call.Spends {
		left := figuresPlanLeft(fig, other)
		if len(left) == 0 {
			continue
		}
		vendor := planVendor(other)
		f.PlanLeftPct, f.PlanVendors = mergePlan(f.PlanLeftPct, f.PlanVendors, f.PlanVendor, left, vendor, less)
		f.PlanEarlyMin, f.PlanEarlyVendors = mergePlan(f.PlanEarlyMin, f.PlanEarlyVendors, f.PlanVendor,
			figuresPlanEarly(fig, other), vendor, more)
		ev = append(ev, eventschema.EvidenceRef{
			Kind: "plan_headroom", Source: "daemon_glance", ObservedAt: fig.ObservedAt,
			FreshUntil: fig.ObservedAt.Add(figuresFreshFor), Confidence: 1, Scope: other,
			Caveat: "the tool call starts " + vendor + ", which spends that plan",
		})
	}
	return ev
}

func less(a, b float64) bool { return a < b }
func more(a, b float64) bool { return a > b }

// mergePlan keeps, for each window, the tighter of the session's plan
// reading and another plan's (the one tighter wins), and whose each one is.
func mergePlan(cur map[domain.Window]float64, vendors map[domain.Window]string, vendor string,
	other map[domain.Window]float64, otherVendor string, tighter func(a, b float64) bool) (map[domain.Window]float64, map[domain.Window]string) {
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
	for w, v := range other {
		if prev, ok := merged[w]; !ok || tighter(v, prev) {
			merged[w], byWindow[w] = v, otherVendor
		}
	}
	return merged, byWindow
}

// figuresPlanLeft is the provider's share left by window, or, with no
// provider, the tightest share each window has across every plan.
func figuresPlanLeft(fig boundaryledger.Figures, provider string) map[domain.Window]float64 {
	return figuresByWindow(fig.PlanLeftPct, provider, less)
}

// figuresPlanEarly is how many minutes ahead of its reset each of the
// provider's windows runs out, or, with no provider, the furthest ahead
// across every plan.
func figuresPlanEarly(fig boundaryledger.Figures, provider string) map[domain.Window]float64 {
	return figuresByWindow(fig.PlanEarlyMin, provider, more)
}

func figuresByWindow(byProvider map[string]map[string]float64, provider string, tighter func(a, b float64) bool) map[domain.Window]float64 {
	out := map[domain.Window]float64{}
	add := func(windows map[string]float64) {
		for name, v := range windows {
			w := domain.Window(name)
			if w != domain.Window5h && w != domain.WindowWeek {
				continue
			}
			if cur, ok := out[w]; !ok || tighter(v, cur) {
				out[w] = v
			}
		}
	}
	if provider != "" {
		add(byProvider[provider])
		return out
	}
	for _, windows := range byProvider {
		add(windows)
	}
	return out
}

// limitsPlan is the share of each Claude plan window left, and how many
// minutes ahead of its reset it runs out on the one forecast, from the
// reading the status line keeps. A window that has reset since is
// unknown, not full: the reading no longer says anything about it.
func limitsPlan(path string, now time.Time) (left, early map[domain.Window]float64, observed time.Time, ok bool) {
	r, found, err := claudelimits.Read(path)
	if err != nil || !found || now.Sub(r.ObservedAt) > limitsFreshFor {
		return nil, nil, time.Time{}, false
	}
	left, early = map[domain.Window]float64{}, map[domain.Window]float64{}
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
		if before, known := vw.RunsOutEarly(now); known {
			early[domain.Window(vw.Name)] = before.Minutes()
		}
	}
	return left, early, r.ObservedAt.UTC(), len(left) > 0
}
