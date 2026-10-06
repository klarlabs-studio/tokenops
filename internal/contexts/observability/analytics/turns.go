package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
	if a == nil || a.store == nil {
		return nil, fmt.Errorf("analytics: aggregator not initialised")
	}
	conds, args := buildConditions(f)
	conds = append(conds, `COALESCE(session_id, '') <> ''`)
	q := `SELECT session_id, timestamp_ns, COALESCE(provider, ''), COALESCE(model, ''),
			COALESCE(input_tokens, 0), COALESCE(output_tokens, 0), COALESCE(total_tokens, 0),
			COALESCE(CAST(COALESCE(json_extract(payload, '$.cached_input_tokens'), json_extract(attributes, '$.cache_read_input')) AS INTEGER), 0),
			COALESCE(cost_usd, 0)
		FROM events WHERE ` + strings.Join(conds, " AND ") + ` ORDER BY timestamp_ns`
	rows, err := a.store.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("analytics: session turns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SessionTurn
	for rows.Next() {
		var (
			t                             SessionTurn
			ns, in, outTok, total, cached int64
			cost                          sql.NullFloat64
		)
		if err := rows.Scan(&t.SessionID, &ns, &t.Provider, &t.Model, &in, &outTok, &total, &cached, &cost); err != nil {
			return nil, fmt.Errorf("analytics: session turns scan: %w", err)
		}
		t.At = time.Unix(0, ns).UTC()
		t.Tokens = total
		if t.Tokens == 0 {
			t.Tokens = in + outTok
		}
		t.CostUSD = cost.Float64
		t.APIEquivalentUSD, t.Priced = t.CostUSD, true
		if a.spend != nil && t.CostUSD == 0 {
			v, err := a.spend.Compute(&eventschema.PromptEvent{
				Provider: eventschema.Provider(t.Provider), RequestModel: t.Model,
				InputTokens: in, CachedInputTokens: cached, OutputTokens: outTok,
			})
			t.APIEquivalentUSD, t.Priced = v, err == nil
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
