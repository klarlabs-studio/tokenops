// Package analytics rolls up the local SQLite event store into the
// time-bucketed aggregates the dashboard, CLI, and forecasting engines
// consume. The aggregator is read-only — it never mutates events. It reads
// through the Store port, which *sqlite.Store implements over the
// (already-indexed) events table, so the same sums can be ported to
// ClickHouse later; pricing, provenance and coverage stay here.
package analytics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Bucket is the discretisation unit for time-bucketed aggregates.
type Bucket string

// Known buckets. Hour and Day cover the dashboards' live-view and
// forecasting windows; minute-resolution can be added later for SLOs.
const (
	BucketHour Bucket = "hour"
	BucketDay  Bucket = "day"
)

// seconds returns the bucket width in seconds.
func (b Bucket) seconds() int64 {
	switch b {
	case BucketDay:
		return 86_400
	default:
		return 3_600
	}
}

// Group identifies the dimension to group by.
type Group string

// Known group dimensions.
const (
	GroupNone     Group = ""
	GroupProvider Group = "provider"
	GroupModel    Group = "model"
	GroupWorkflow Group = "workflow"
	GroupAgent    Group = "agent"
)

// Filter narrows the events the aggregator considers. Empty fields are
// not constrained.
//
// ExcludeSources gates activity-proxy surfaces. nil
// means "apply DefaultExcludedSources". An empty non-nil slice means
// "include every source".
//
// IncludeSources re-admits named entries of DefaultExcludedSources
// without disturbing the rest. It is ignored when
// ExcludeSources is non-nil, because an explicit exclude list already
// states exactly what to drop.
type Filter struct {
	EventType      eventschema.EventType
	Provider       string
	Model          string
	WorkflowID     string
	AgentID        string
	Since          time.Time
	Until          time.Time
	ExcludeSources []string
	IncludeSources []string
}

// DefaultExcludedSources is applied by every analytics query unless the
// caller passes a non-nil ExcludeSources slice. `mcp-session` is an
// activity-proxy ping the MCP server records about itself, which would
// otherwise inflate the request count an operator reads as "calls I made".
// Opt it back in per-source via
// `--include-source=` / `include_sources: [...]`.
var DefaultExcludedSources = []string{"mcp-session"}

// ExcludedSources returns the operative exclude list for a Filter:
// caller-supplied slice when set (including empty for "show everything"),
// otherwise the package default minus anything the caller re-admitted via
// IncludeSources. Store implementations apply it to every query.
func (f Filter) ExcludedSources() []string {
	if f.ExcludeSources != nil {
		return f.ExcludeSources
	}
	if len(f.IncludeSources) == 0 {
		return DefaultExcludedSources
	}
	readmit := make(map[string]bool, len(f.IncludeSources))
	for _, s := range f.IncludeSources {
		readmit[s] = true
	}
	out := make([]string, 0, len(DefaultExcludedSources))
	for _, s := range DefaultExcludedSources {
		if !readmit[s] {
			out = append(out, s)
		}
	}
	return out
}

// Row is one (bucket, group-key) cell of an aggregate.
type Row struct {
	BucketStart  time.Time
	GroupKey     string
	Requests     int64
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	CostUSD      float64
	// APIEquivalentUSD is what this row would have billed at API list
	// prices: CostUSD plus a list-price recompute of its plan-included
	// and trial traffic (which is $0 real). For metered rows it equals
	// CostUSD.
	//
	// Without it a flat-rate deployment's per-group table was a column of
	// $0.0000 under a headline reporting thousands, and "top consumers"
	// ranked every consumer equal. The summary had carried this figure
	// since it was added; the rows it breaks down never did.
	APIEquivalentUSD float64
	// CostRecomputed reports the number of rows in this bucket whose
	// CostUSD was 0 in the store and was recomputed via spend.Engine.
	// Useful for dashboards to flag stale pricing tables.
	CostRecomputed int64
	// Cost is CostUSD with its provenance attached: whether the figure
	// was observed or recomputed from a rate card, and how many events it
	// could not account for.
	//
	// CostUSD alone cannot express either. An unpriced model used to
	// contribute 0 to it and say nothing, so a bucket containing a model
	// TokenOps has no rate card for reported a total that looked
	// complete — on the surfaces that feed burn rate, forecast, top
	// consumers and the dashboard. Summarize reported that gap as
	// Unpriced; AggregateBy, which is what those surfaces call, did not.
	//
	// CostUSD stays as it was for callers that have not migrated. Read
	// Cost to learn whether the number is worth acting on.
	Cost measurement.Value
	// APIEquivalent is APIEquivalentUSD with its provenance attached.
	// Always derived: nobody was charged it, and the same unpriced-model
	// gap applies to the plan-covered recompute that builds it.
	APIEquivalent measurement.Value
}

// Summary is the global rollup over a query: total requests / tokens /
// cost across the entire filter window. It is what the CLI prints as
// "this week's spend" and the dashboard surfaces as headline numbers.
type Summary struct {
	Requests     int64
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	CostUSD      float64
	// APIEquivalentUSD is what the window would have billed at API list
	// prices: CostUSD plus a list-price recompute of plan-included and
	// trial traffic (which is $0 real). For metered-only deployments it
	// equals CostUSD; for flat-plan deployments it is the shadow value
	// the subscription absorbed.
	APIEquivalentUSD float64
	// Unpriced lists (provider, model) pairs in the window with no rate in
	// the pricing table. For metered events their actual cost may be absent
	// from CostUSD; for plan-covered events actual cost remains zero while
	// the API-equivalent estimate is incomplete.
	Unpriced []UnpricedModel `json:",omitempty"`
}

// UnpricedModel identifies a model whose usage could not be costed.
type UnpricedModel struct {
	Provider string
	Model    string
	Requests int64
}

// Aggregator answers rollup queries against the event store, through the
// Store port. spend.Engine is consulted when a row's CostUSD is zero (e.g.
// older events written before the spend engine was wired in).
type Aggregator struct {
	store Store
	spend *spend.Engine
}

// New constructs an Aggregator over store (a *sqlite.Store in production).
// spendEng may be nil — rows with zero cost then stay zero rather than
// being recomputed.
func New(store Store, spendEng *spend.Engine) *Aggregator {
	return &Aggregator{store: store, spend: spendEng}
}

// ready reports whether a has a store to read.
func (a *Aggregator) ready() bool { return a != nil && a.store != nil }

// AggregateBy returns time-bucketed aggregates. Setting group to GroupNone
// produces one row per bucket (across all events in the bucket).
func (a *Aggregator) AggregateBy(ctx context.Context, f Filter, bucket Bucket, group Group) ([]Row, error) {
	if !a.ready() {
		return nil, ErrNotInitialised
	}
	width := bucket.seconds()
	if width <= 0 {
		return nil, fmt.Errorf("analytics: invalid bucket %q", bucket)
	}
	// One read of the window answers the rows and both pricing passes.
	usage, err := a.store.BucketUsage(ctx, f, width, group)
	if err != nil {
		return nil, err
	}
	out := usage.Rows

	// Seed provenance from the store. recomputeMissingCosts replaces this
	// for rows it prices; rows it does not reach keep the stored figure,
	// and rows nothing can price stay unknown rather than zero.
	for i := range out {
		out[i].Cost = storedCostValue(out[i], a.spend != nil)
		out[i].APIEquivalent = out[i].Cost
	}

	if a.spend != nil {
		a.recomputeMissingCosts(usage.Uncosted, out)
		a.addPlanCoveredValue(usage.PlanCovered, out)
	}
	return out, nil
}

// rowKey identifies an AggregateBy row: its bucket and group key.
type rowKey struct {
	bucketSec int64
	groupKey  string
}

// promptEvent is the usage of g as a PromptEvent, for the spend engine.
func (g PricingGroup) promptEvent() *eventschema.PromptEvent {
	return &eventschema.PromptEvent{
		Provider:                eventschema.Provider(g.Provider),
		RequestModel:            g.Model,
		InputTokens:             g.InputTokens,
		CachedInputTokens:       g.CacheReadTokens,
		CacheWriteInputTokens:   g.CacheWriteTokens,
		CacheWrite1hInputTokens: g.CacheWrite1hTokens,
		OutputTokens:            g.OutputTokens,
	}
}

// addPlanCoveredValue fills Row.APIEquivalentUSD: each row's real cost plus
// the list price of the plan-covered traffic inside it.
//
// groups are the window's plan-covered pricing groups, grouped on the same
// bucket and key as the rows, so the rows can only sum to the figure
// Summarize reports for the same window — the two are renderings of one
// window and disagreeing is the bug this fixes.
func (a *Aggregator) addPlanCoveredValue(groups []PricingGroup, rows []Row) {
	if len(rows) == 0 {
		return
	}
	for i := range rows {
		rows[i].APIEquivalentUSD = rows[i].CostUSD
	}
	// provider+model are carried alongside the grouping because pricing is
	// keyed by them: grouping by workflow still has to price each model the
	// workflow ran.
	value := map[rowKey]float64{}
	gaps := newGapTracker[rowKey]()
	for _, g := range groups {
		k := rowKey{g.At.Unix(), g.GroupKey}
		// An unpriced model contributes nothing rather than failing the
		// query — failing the whole rollup over one missing rate card would
		// take the figures that do work with it. The events it could not
		// price are counted against the row's coverage so the shadow value
		// stops presenting itself as the whole of the plan-covered traffic.
		c, cerr := a.spend.ComputeAt(g.promptEvent(), g.At)
		if cerr != nil {
			gaps.add(k, g.Events, unpricedReason(g.Provider, g.Model))
			continue
		}
		value[k] += c
	}

	for i := range rows {
		k := rowKey{rows[i].BucketStart.Unix(), rows[i].GroupKey}
		rows[i].APIEquivalentUSD += value[k]
		// The equivalent inherits the cost figure's gaps as well as its
		// own: it is built on top of CostUSD, so an event the metered
		// recompute could not price is missing from both.
		missing, why := gaps.at(k)
		costMissing, costWhy := rows[i].Cost.Coverage().Excluded, rows[i].Cost.Coverage().Reasons
		rows[i].APIEquivalent = equivalentValue(rows[i], missing+costMissing, mergeReasons(costWhy, why))
	}
}

// recomputeMissingCosts prices the metered events stored without a cost and
// adds them to the row they belong to, counting each in CostRecomputed.
// Stored costs stay authoritative; only the zeros are filled.
//
// groups are the window's uncosted metered pricing groups, grouped on the
// same bucket and key as the rows — the shape addPlanCoveredValue has — so
// rows can only sum to what Summarize reports for the window. The per-row version it replaces recomputed over a
// fixed one-hour window whatever the bucket (a day row was re-priced only
// for its first hour), ignored the row's group (each unpriced model's row
// was charged for every model in the bucket), and skipped any row with some
// stored cost, leaving its zero-cost events out.
func (a *Aggregator) recomputeMissingCosts(groups []PricingGroup, rows []Row) {
	if len(rows) == 0 {
		return
	}
	cost := map[rowKey]float64{}
	fixed := map[rowKey]int64{}
	// Events whose model has no rate card, per row, with the reasons.
	gaps := newGapTracker[rowKey]()
	for _, g := range groups {
		k := rowKey{g.At.Unix(), g.GroupKey}
		// Price at the rate card in effect for this bucket (ADR 0002
		// Phase 2). An unpriced model still contributes nothing to the
		// total — failing the whole rollup over one missing rate card
		// would take the figures that do work with it — but the events
		// it could not price are now counted against the row's coverage
		// instead of vanishing.
		c, cerr := a.spend.ComputeAt(g.promptEvent(), g.At)
		if cerr != nil {
			gaps.add(k, g.Events, unpricedReason(g.Provider, g.Model))
			continue
		}
		cost[k] += c
		fixed[k] += g.Events
	}
	for i := range rows {
		k := rowKey{rows[i].BucketStart.Unix(), rows[i].GroupKey}
		rows[i].CostUSD += cost[k]
		rows[i].CostRecomputed += fixed[k]
		missing, why := gaps.at(k)
		rows[i].Cost = recomputedCostValue(rows[i], missing, why)
	}
}

// Summarize returns a single global rollup over the filter window. It is
// equivalent to AggregateBy with an unbounded bucket, and like it reads
// the window once.
func (a *Aggregator) Summarize(ctx context.Context, f Filter) (Summary, error) {
	if !a.ready() {
		return Summary{}, ErrNotInitialised
	}
	usage, err := a.store.WindowUsage(ctx, f)
	if err != nil {
		return Summary{}, err
	}
	s := usage.Totals
	if a.spend != nil {
		recomputed, unpriced := a.summarizeMissingCost(usage.Uncosted)
		s.CostUSD += recomputed
		s.Unpriced = unpriced
		planValue, planUnpriced := a.summarizePlanCoveredValue(usage.PlanCovered)
		s.Unpriced = mergeUnpriced(s.Unpriced, planUnpriced)
		s.APIEquivalentUSD = s.CostUSD + planValue
	} else {
		s.APIEquivalentUSD = s.CostUSD
	}
	return s, nil
}

// The summary totals price each (day, provider, model) group at the rate
// card in effect at the group's latest event (WindowUsage's groups).
// Pricing an all-window aggregate with no timestamp would select the
// oldest card: a model a later refresh added would read as unpriced, a
// repriced one at its old price. A day is the finest grain a rate card
// changes at in practice.

// addUnpriced records requests for a model that could not be priced,
// merging the per-day groups back into one entry per (provider, model).
func addUnpriced(list []UnpricedModel, provider, model string, requests int64) []UnpricedModel {
	for i := range list {
		if list[i].Provider == provider && list[i].Model == model {
			list[i].Requests += requests
			return list
		}
	}
	return append(list, UnpricedModel{Provider: provider, Model: model, Requests: requests})
}

// summarizePlanCoveredValue recomputes plan-included / trial traffic at
// list prices — the shadow value a flat-rate subscription absorbed — and
// returns it with any (provider, model) pairs it could not price. The
// second return matters on a flat plan: an unpriced model contributes
// nothing to the shadow value, and because its real cost is legitimately
// zero it would otherwise leave no trace at all. Mirrors
// summarizeMissingCost with the cost-source selection inverted.
func (a *Aggregator) summarizePlanCoveredValue(groups []PricingGroup) (float64, []UnpricedModel) {
	var (
		total    float64
		unpriced []UnpricedModel
	)
	for _, g := range groups {
		c, err := a.spend.ComputeAt(g.promptEvent(), g.At)
		if err != nil {
			// tokenops' own telemetry (MCP session pings and friends)
			// carries a pseudo-model that will never have a rate card.
			// It is not a model call, so it is not a pricing gap.
			if isSelfTelemetryModel(g.Model) {
				continue
			}
			// No rate card for a model the operator actually ran.
			// Record it rather than discarding the error — silently
			// dropping the row is how a subscription's dominant model can
			// be missing from the api-equivalent figure with nothing to
			// show for it.
			unpriced = addUnpriced(unpriced, g.Provider, g.Model, g.Events)
			continue
		}
		total += c
	}
	return total, unpriced
}

// CacheStatsResult is the per-window cache split. Token counts are
// summed across the filter range; CacheRatio is the share of input
// tokens that came from cache reads (0..1).
type CacheStatsResult struct {
	TotalInputTokens    int64   `json:"total_input_tokens"`
	CachedInputTokens   int64   `json:"cached_input_tokens"`
	UncachedInputTokens int64   `json:"uncached_input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheRatio          float64 `json:"cache_ratio"`
}

// CacheStats sums cached vs uncached input over the filter window.
func (a *Aggregator) CacheStats(ctx context.Context, f Filter) (CacheStatsResult, error) {
	if !a.ready() {
		return CacheStatsResult{}, ErrNotInitialised
	}
	t, err := a.store.CacheTotals(ctx, f)
	if err != nil {
		return CacheStatsResult{}, err
	}
	s := CacheStatsResult{
		TotalInputTokens:  t.InputTokens,
		CachedInputTokens: t.CachedInputTokens,
		OutputTokens:      t.OutputTokens,
	}
	s.UncachedInputTokens = s.TotalInputTokens - s.CachedInputTokens
	if s.UncachedInputTokens < 0 {
		s.UncachedInputTokens = 0
	}
	if s.TotalInputTokens > 0 {
		s.CacheRatio = float64(s.CachedInputTokens) / float64(s.TotalInputTokens)
	}
	return s, nil
}

// summarizeMissingCost computes the spend.Engine cost for events whose
// stored cost_usd is zero — the case for vendor-usage-jsonl sources that
// ship token counts but not prices. Groups by (provider, model) so one
// engine.Compute call covers the entire group per model, which is
// linear-in-tokens and matches the per-event sum exactly. Cached input
// tokens are summed separately so cache-heavy workloads get the lower
// cache rate instead of being billed at the new-input rate.
//
// Models the pricing table doesn't know are returned as UnpricedModel
// entries instead of being silently dropped — their cost stays absent
// from the total, and callers surface that gap as a warning.
func (a *Aggregator) summarizeMissingCost(groups []PricingGroup) (float64, []UnpricedModel) {
	var (
		total    float64
		unpriced []UnpricedModel
	)
	for _, g := range groups {
		c, err := a.spend.ComputeAt(g.promptEvent(), g.At)
		switch {
		case err == nil:
			total += c
		case errors.Is(err, spend.ErrUnknownModel):
			unpriced = addUnpriced(unpriced, g.Provider, g.Model, g.Events)
		}
	}
	return total, unpriced
}

// selfTelemetryModels are pseudo-models tokenops emits about itself.
// They describe daemon activity, not an LLM call, so they will never
// have a rate card and must not be reported as pricing gaps.
var selfTelemetryModels = map[string]bool{"mcp-session": true}

func isSelfTelemetryModel(model string) bool { return selfTelemetryModels[model] }

// mergeUnpriced folds two unpriced lists into one, summing requests for
// pairs that appear in both (a model can be partly metered and partly
// plan-covered inside one window).
func mergeUnpriced(a, b []UnpricedModel) []UnpricedModel {
	if len(b) == 0 {
		return a
	}
	idx := make(map[string]int, len(a)+len(b))
	out := make([]UnpricedModel, 0, len(a)+len(b))
	add := func(u UnpricedModel) {
		k := u.Provider + "/" + u.Model
		if i, ok := idx[k]; ok {
			out[i].Requests += u.Requests
			return
		}
		idx[k] = len(out)
		out = append(out, u)
	}
	for _, u := range a {
		add(u)
	}
	for _, u := range b {
		add(u)
	}
	return out
}
