package headroom

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// EventReader is what WindowPressure reads: the vendor's window readings
// and the plan-included prompts.
type EventReader interface {
	ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error)
}

// WindowPressure is how full provider's rate-limit window is, in percent:
// the vendor's own reading when there is a recent one, else the plan's
// message cap against the messages counted in the window.
//
// The vendor's reading wins wherever one exists: Codex publishes rate
// limits per turn, Copilot and Cursor publish quota snapshots, and those
// are what the operator sees in their own dashboard. Counting messages
// against the catalog cap is the heuristic for clients that publish
// nothing.
//
// The daemon's router and the MCP routing advice both ask this. They
// each had a copy, and the advice's computed a source breakdown it then
// threw away.
//
// Unknown rather than zero on every failure. A router rule gated on
// window pressure must not fire on a default: this meter read 0/200 for
// months on a real machine, and acting on that would have degraded
// quality to relieve a shortage that was not happening.
func WindowPressure(ctx context.Context, cfg *config.Config, r EventReader, provider eventschema.Provider, now time.Time) (float64, bool) {
	if r == nil || cfg == nil {
		return 0, false
	}
	plan, ok := plans.Lookup(cfg.Plans[string(provider)])
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
	return float64(win.MessagesInWindow) / float64(plan.MessagesPerWindow) * 100, true
}
