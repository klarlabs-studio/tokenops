package plans

import (
	"context"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
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
	// FirstActivityAt is the earliest plan-covered event inside the
	// window, zero when there is none. A rolling window opens with its
	// first message, so this is what an estimated reset is measured from.
	FirstActivityAt time.Time
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
		if out.FirstActivityAt.IsZero() || env.Timestamp.Before(out.FirstActivityAt) {
			out.FirstActivityAt = env.Timestamp
		}
		if countsAsMessage(env) {
			out.MessagesInWindow++
		}
		tokens := p.TotalTokens
		if tokens == 0 {
			tokens = p.InputTokens + p.OutputTokens
		}
		out.TokensInWindow += tokens
	}
	return out, nil
}

// countsAsMessage reports whether an event approximates one entry on
// the vendor's "messages" meter (a user prompt). Sources that emit
// finer- or coarser-grained events declare it via the granularity
// attribute: the claude-code / codex JSONL readers emit one event per
// ASSISTANT TURN (a single prompt fans out into many tool-use turns)
// and the legacy stats-cache poller emits one event per (day, model).
// Counting those against a 200-messages-per-window cap would inflate
// the meter by an order of magnitude; their tokens still count.
func countsAsMessage(env *eventschema.Envelope) bool {
	// A per-turn source that can identify which turn answered a typed
	// prompt reports it explicitly. That flag is the vendor's "messages"
	// unit, so it wins over the coarse granularity check below: without
	// it an assistant_turn stream contributes nothing at all and the
	// window meter reads 0 forever.
	if v, ok := env.Attributes["starts_user_message"]; ok {
		return v == "true"
	}
	switch env.Attributes["granularity"] {
	case "assistant_turn", "daily", "bucket", "quota_snapshot", "monthly_snapshot":
		return false
	default:
		return true
	}
}

// SpendWindowStart returns the start of the period a spend limit covers.
// An unrecognised or empty window means monthly, which is how vendor
// consoles state org limits.
func SpendWindowStart(now time.Time, window string) time.Time {
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "daily", "day":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	case "weekly", "week":
		offset := (int(now.Weekday()) + 6) % 7 // ISO weeks start Monday
		d := now.AddDate(0, 0, -offset)
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
	default:
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
}

// SpendInWindow sums real billed cost for a provider over the spend window.
//
// It reads CostUSD rather than an API-equivalent: a spend-denominated plan
// is billed at API rates from the first token, so the cost carried on the
// event IS what the vendor charges. A plan-covered event contributes zero,
// which is correct — that traffic is not billed.
func SpendInWindow(ctx context.Context, reader EventReader, provider string, now time.Time, window string) (float64, error) {
	events, err := reader.ReadEvents(ctx, eventschema.EventTypePrompt, SpendWindowStart(now, window))
	if err != nil {
		return 0, err
	}
	var total float64
	for _, env := range events {
		if env == nil {
			continue
		}
		p, ok := env.Payload.(*eventschema.PromptEvent)
		if !ok || string(p.Provider) != provider {
			continue
		}
		total += p.CostUSD
	}
	return total, nil
}
