package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// planStampSink is the single choke point that implements the config
// contract "requests routed to a provider with a configured plan are
// billed as plan_included": every PromptEvent flowing to the store
// with an empty CostSource gets stamped when its provider has a plan
// bound. Stamping here (rather than in each emitter) means new pollers
// and the proxy observer can't silently reintroduce phantom list-price
// spend — the per-poller CostSource options remain for explicit
// overrides (e.g. trial), which this sink never touches.
type planStampSink struct {
	next    events.Sink
	planned map[eventschema.Provider]bool
}

// newPlanStampSink wraps next. With no plans configured it returns
// next unchanged so the hot path pays nothing.
func newPlanStampSink(next events.Sink, cfg config.Config) events.Sink {
	if len(cfg.Plans) == 0 {
		return next
	}
	planned := make(map[eventschema.Provider]bool, len(cfg.Plans))
	for provider := range cfg.Plans {
		if cfg.PlanCovers(provider) {
			planned[eventschema.Provider(provider)] = true
		}
	}
	if len(planned) == 0 {
		return next
	}
	return &planStampSink{next: next, planned: planned}
}

// AppendBatch stamps in place before forwarding. Envelopes are owned by
// the bus worker at this point, so mutation is race-free.
func (s *planStampSink) AppendBatch(ctx context.Context, envs []*eventschema.Envelope) error {
	for _, env := range envs {
		if env == nil || env.Type != eventschema.EventTypePrompt {
			continue
		}
		p, ok := env.Payload.(*eventschema.PromptEvent)
		if !ok || p.CostSource != "" {
			continue
		}
		if s.planned[p.Provider] {
			p.CostSource = eventschema.CostSourcePlanIncluded
		}
	}
	return s.next.AppendBatch(ctx, envs)
}

// correctSpendCoverage re-marks usage recorded as covered under a plan
// billed at API rates (ADR 0009 §4). It runs at start, which also follows
// every plan change, since `plan set` restarts the daemon. A failure is
// logged and never stops the daemon: the usage stays as recorded and the
// next start tries again.
func correctSpendCoverage(ctx context.Context, cfg config.Config, store *sqlite.Store, logger *slog.Logger) {
	if store == nil {
		return
	}
	fixed, err := planhistory.CorrectSpendCoverage(ctx, store, cfg.Plans, time.Now().UTC())
	for _, c := range fixed {
		logger.Info("re-marked usage under a plan billed at API rates as billed",
			"provider", c.Provider, "plan", c.Plan, "calls", c.Restamped)
	}
	if err != nil {
		logger.Warn("spend-plan correction failed; will retry at next start", "err", err)
	}
}
