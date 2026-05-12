package plans

import (
	"context"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// EventReader is the read-side port consumption tallies depend on.
// sqlite-backed adapters implement it inside the CLI / MCP packages
// so this package never imports infrastructure.
type EventReader interface {
	ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error)
}

// Consumption captures plan-included token totals for the headroom
// calculator. Month-to-date drives ConsumedTokens; the last seven days
// of activity drive Last7DayTokens (the burn-rate denominator).
type Consumption struct {
	ConsumedTokens int64
	Last7DayTokens int64
}

// WindowConsumption is the rolling-window counterpart to Consumption.
// MessagesInWindow is the count of plan-included PromptEvents in the
// trailing RateLimitWindow; TokensInWindow rolls up the same events'
// token totals for callers that want a token-based ratio.
type WindowConsumption struct {
	MessagesInWindow int64
	TokensInWindow   int64
}

// ConsumptionFor sums plan_included PromptEvent tokens for the given
// provider over the current calendar month + a rolling 7-day window.
// Events without a CostSource (or set to metered/trial) are ignored.
// The `now` parameter lets tests pin the clock; production passes
// time.Now().UTC().
func ConsumptionFor(ctx context.Context, r EventReader, provider string, now time.Time) (Consumption, error) {
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	since := monthStart
	if w := now.AddDate(0, 0, -7); w.Before(monthStart) {
		// Always read at least the rolling-7-day window so the burn
		// rate is computable on the first days of a new month.
		since = w
	}

	envs, err := r.ReadEvents(ctx, eventschema.EventTypePrompt, since)
	if err != nil {
		return Consumption{}, err
	}

	weekCutoff := now.Add(-7 * 24 * time.Hour)
	var out Consumption
	for _, env := range envs {
		p, ok := env.Payload.(*eventschema.PromptEvent)
		if !ok {
			continue
		}
		if p.CostSource != eventschema.CostSourcePlanIncluded {
			continue
		}
		if string(p.Provider) != provider {
			continue
		}
		tokens := p.TotalTokens
		if tokens == 0 {
			tokens = p.InputTokens + p.OutputTokens
		}
		if !env.Timestamp.Before(monthStart) {
			out.ConsumedTokens += tokens
		}
		if !env.Timestamp.Before(weekCutoff) {
			out.Last7DayTokens += tokens
		}
	}
	return out, nil
}

// ConsumptionInWindow tallies plan-included PromptEvents for the given
// provider over the trailing `window`. Window <= 0 returns a zero
// report — callers should branch on Plan.RateLimitWindow > 0 before
// invoking. The reader sees events going back to `window`, so the
// returned counts exhaustively cover that span.
func ConsumptionInWindow(ctx context.Context, r EventReader, provider string, now time.Time, window time.Duration) (WindowConsumption, error) {
	var out WindowConsumption
	if window <= 0 {
		return out, nil
	}
	cutoff := now.Add(-window)
	envs, err := r.ReadEvents(ctx, eventschema.EventTypePrompt, cutoff)
	if err != nil {
		return out, err
	}
	for _, env := range envs {
		p, ok := env.Payload.(*eventschema.PromptEvent)
		if !ok {
			continue
		}
		if p.CostSource != eventschema.CostSourcePlanIncluded {
			continue
		}
		if string(p.Provider) != provider {
			continue
		}
		if env.Timestamp.Before(cutoff) {
			continue
		}
		out.MessagesInWindow++
		tokens := p.TotalTokens
		if tokens == 0 {
			tokens = p.InputTokens + p.OutputTokens
		}
		out.TokensInWindow += tokens
	}
	return out, nil
}
