package daemon

import (
	"context"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/events"
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
	for provider, plan := range cfg.Plans {
		if plan != "" {
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
