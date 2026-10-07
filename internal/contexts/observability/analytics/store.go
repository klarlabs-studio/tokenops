package analytics

import (
	"context"
	"errors"
	"time"
)

// ErrNotInitialised reports an Aggregator with no store to read. A Store
// implementation returns it too when it is itself nil (a nil *sqlite.Store
// passed to New), so the two read the same to a caller.
var ErrNotInitialised = errors.New("analytics: aggregator not initialised")

// Store is the port the aggregator reads the event store through.
// *sqlite.Store implements it over the events table. It answers sums and
// groupings only; pricing, provenance and coverage are the aggregator's.
//
// Every method narrows to the prompt events that carry usage, by the
// Filter, with Filter.ExcludedSources applied.
type Store interface {
	// UsageBuckets returns one Row per (bucket, group key), in that
	// order, with BucketStart, GroupKey, Requests, the token sums and the
	// stored CostUSD set. widthSec is the bucket width.
	UsageBuckets(ctx context.Context, f Filter, widthSec int64, group Group) ([]Row, error)
	// BucketPricingGroups sums the events sel selects per (bucket, group
	// key, provider, model). PricingGroup.At is the bucket start.
	BucketPricingGroups(ctx context.Context, f Filter, widthSec int64, group Group, sel CostSelection) ([]PricingGroup, error)
	// UsageTotals returns Requests, the token sums and the stored CostUSD
	// over the whole window.
	UsageTotals(ctx context.Context, f Filter) (Summary, error)
	// DailyPricingGroups sums the events sel selects per (UTC day,
	// provider, model). PricingGroup.At is the group's latest event: the
	// rate card in effect then prices it.
	DailyPricingGroups(ctx context.Context, f Filter, sel CostSelection) ([]PricingGroup, error)
	// CacheTotals sums input, cache-read input and output tokens.
	CacheTotals(ctx context.Context, f Filter) (CacheTotals, error)
	// SessionUsage lists each event that belongs to a session, oldest
	// first.
	SessionUsage(ctx context.Context, f Filter) ([]SessionUsage, error)
}

// CostSelection picks which events a pricing query sums.
type CostSelection int

const (
	// UncostedMetered is metered traffic stored without a cost: what the
	// aggregator recomputes from the rate card. Plan-included and trial
	// events are zero-cost by design and never repriced as spend.
	UncostedMetered CostSelection = iota + 1
	// PlanCovered is plan-included and trial traffic: what the
	// API-equivalent figure values at list price.
	PlanCovered
)

// PricingGroup is the usage of one provider and model inside a group,
// summed so a single rate-card lookup prices all of it.
type PricingGroup struct {
	// At is when the group is priced: its bucket start, or for a daily
	// group its latest event.
	At       time.Time
	GroupKey string
	Provider string
	Model    string
	Events   int64
	// InputTokens includes the cache portions below.
	InputTokens, OutputTokens         int64
	CacheReadTokens, CacheWriteTokens int64
	CacheWrite1hTokens                int64
}

// CacheTotals is the cache split of a window's input.
type CacheTotals struct {
	InputTokens, CachedInputTokens, OutputTokens int64
}

// SessionUsage is one session event's usage as stored.
type SessionUsage struct {
	SessionID string
	At        time.Time
	Provider  string
	Model     string
	// InputTokens includes the cache portions below.
	InputTokens, OutputTokens, TotalTokens int64
	CacheReadTokens, CacheWriteTokens      int64
	CacheWrite1hTokens                     int64
	// CostUSD is the stored cost, 0 when none was stored.
	CostUSD float64
}
