package headroom

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// WindowPressure is how full provider's rate-limit window is, in percent:
// the vendor's own reading when there is a recent one, else the plan's
// message cap against the messages counted in the window.
//
// Unknown rather than zero on every failure. A router rule gated on
// window pressure must not fire on a default: this meter read 0/200 for
// months on a real machine, and acting on that would have degraded
// quality to relieve a shortage that was not happening.
func WindowPressure(ctx context.Context, cfg *config.Config, r Reader, provider eventschema.Provider, now time.Time) (float64, bool) {
	if r == nil || cfg == nil {
		return 0, false
	}
	planName, ok := cfg.Plans[string(provider)]
	if !ok {
		return 0, false
	}
	plan, ok := plans.Lookup(planName)
	if !ok || plan.RateLimitWindow <= 0 {
		return 0, false
	}
	if a := plans.LatestAuthoritativeWindow(ctx, r, provider, plan, now); a != nil {
		return a.UsedPct, true
	}
	if plan.MessagesPerWindow <= 0 {
		return 0, false
	}
	win, err := plans.ConsumptionInWindow(ctx, r, string(provider), now, plan.RateLimitWindow)
	if err != nil {
		return 0, false
	}
	counts, err := r.CountBySource(ctx, now.Add(-plan.RateLimitWindow), now)
	if err != nil {
		return 0, false
	}
	report, err := plans.ComputeHeadroom(planName, plans.HeadroomInputs{
		Now:            now,
		WindowMessages: win.MessagesInWindow,
		Signal:         plans.SignalFromCounts(counts, string(provider)),
	})
	if err != nil {
		return 0, false
	}
	return report.WindowPct, true
}
