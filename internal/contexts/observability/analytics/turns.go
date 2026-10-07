package analytics

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SessionTurn is one priced model call within a session.
type SessionTurn struct {
	SessionID string
	At        time.Time
	Provider  string
	Model     string
	Tokens    int64
	// CostUSD is what was billed; APIEquivalentUSD what it costs at list
	// prices, its value where a plan covers it. Priced is false when the
	// model has no list price, so neither figure is complete.
	CostUSD          float64
	APIEquivalentUSD float64
	Priced           bool
}

// SessionTurns lists every usage-carrying prompt event in f that belongs
// to a session, priced. It is what attributes cost to work outside the
// event store — a commit, a branch — by joining on the session.
func (a *Aggregator) SessionTurns(ctx context.Context, f Filter) ([]SessionTurn, error) {
	if !a.ready() {
		return nil, ErrNotInitialised
	}
	usage, err := a.store.SessionUsage(ctx, f)
	if err != nil {
		return nil, err
	}
	var out []SessionTurn
	for _, u := range usage {
		t := SessionTurn{SessionID: u.SessionID, At: u.At, Provider: u.Provider, Model: u.Model}
		t.Tokens = u.TotalTokens
		if t.Tokens == 0 {
			t.Tokens = u.InputTokens + u.OutputTokens
		}
		t.CostUSD = u.CostUSD
		t.APIEquivalentUSD, t.Priced = t.CostUSD, true
		if a.spend != nil && t.CostUSD == 0 {
			// Priced at the card in effect at the turn, not the oldest one.
			v, err := a.spend.ComputeAt(&eventschema.PromptEvent{
				Provider: eventschema.Provider(t.Provider), RequestModel: t.Model,
				InputTokens: u.InputTokens, CachedInputTokens: u.CacheReadTokens, OutputTokens: u.OutputTokens,
				CacheWriteInputTokens: u.CacheWriteTokens, CacheWrite1hInputTokens: u.CacheWrite1hTokens,
			}, t.At)
			t.APIEquivalentUSD, t.Priced = v, err == nil
		}
		out = append(out, t)
	}
	return out, nil
}
