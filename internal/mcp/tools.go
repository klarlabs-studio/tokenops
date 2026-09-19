package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Deps wires the engines the TokenOps MCP tools query against. Pass a
// shared *sqlite.Store and reusable engines so opening / closing
// happens at the daemon level.
type Deps struct {
	Store      *sqlite.Store
	Aggregator *analytics.Aggregator
	Spend      *spend.Engine
	// Waste configures the workflow waste detector (operator context
	// limits from coaching.context_limits). Zero value uses defaults.
	Waste waste.Config
	// WasteConfig, when set, supplies it at call time so an edit to
	// coaching.context_limits applies without restarting the MCP client.
	// It wins over Waste.
	WasteConfig func() waste.Config

	// StaleSources reports vendor-usage sources that have stopped
	// ingesting. Optional: nil means the caveat is omitted, which is the
	// behaviour before spend answers carried one. Supplied by the daemon,
	// the same hook ControlDeps uses.
	StaleSources func() []config.StaleSource
}

// --- input structs --------------------------------------------------------

type spendSummaryInput struct {
	Since          string   `json:"since,omitempty" jsonschema:"description=RFC3339 timestamp or duration like '24h' or '7d'"`
	Until          string   `json:"until,omitempty" jsonschema:"description=RFC3339 timestamp"`
	WorkflowID     string   `json:"workflow_id,omitempty"`
	AgentID        string   `json:"agent_id,omitempty"`
	IncludeSources []string `json:"include_sources,omitempty" jsonschema:"description=re-admit event sources excluded by default: 'demo' (synthetic seeds from tokenops demo) and/or 'mcp-session' (MCP activity-proxy pings)"`
	IncludeDemo    bool     `json:"include_demo,omitempty" jsonschema:"description=alias for include_sources: [demo]"`
}

type topConsumersInput struct {
	By             string   `json:"by,omitempty" jsonschema:"enum=model,enum=provider,enum=workflow,enum=agent"`
	Top            int      `json:"top,omitempty" jsonschema:"minimum=1,maximum=50"`
	Since          string   `json:"since,omitempty"`
	Until          string   `json:"until,omitempty"`
	IncludeSources []string `json:"include_sources,omitempty"`
	IncludeDemo    bool     `json:"include_demo,omitempty"`
}

type burnRateInput struct {
	Hours          int      `json:"hours,omitempty" jsonschema:"minimum=1,maximum=168"`
	IncludeSources []string `json:"include_sources,omitempty"`
	IncludeDemo    bool     `json:"include_demo,omitempty"`
}

type forecastInput struct {
	HorizonDays    int      `json:"horizon_days,omitempty" jsonschema:"minimum=1,maximum=30"`
	IncludeSources []string `json:"include_sources,omitempty"`
	IncludeDemo    bool     `json:"include_demo,omitempty"`
}

type workflowTraceInput struct {
	WorkflowID string `json:"workflow_id" jsonschema:"required"`
}

type optimizationsInput struct {
	Since      string `json:"since,omitempty"`
	Until      string `json:"until,omitempty"`
	WorkflowID string `json:"workflow_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// --- output structs -------------------------------------------------------

// unpricedModel names a (provider, model) pair whose events carry no
// stored cost and have no rate in the pricing table.
type unpricedModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Requests int64  `json:"requests"`
}

// pricingWarning flags that cost_usd underestimates spend because some
// models in the window are unpriced.
type pricingWarning struct {
	Message        string          `json:"message"`
	UnpricedModels []unpricedModel `json:"unpriced_models"`
}

// spendSummaryResult is the typed payload for tokenops_spend_summary.
type spendSummaryResult struct {
	Window           spendSummaryInput `json:"window"`
	Requests         int64             `json:"requests"`
	InputTokens      int64             `json:"input_tokens"`
	OutputTokens     int64             `json:"output_tokens"`
	TotalTokens      int64             `json:"total_tokens"`
	CostUSD          float64           `json:"cost_usd"`
	APIEquivalentUSD float64           `json:"api_equivalent_usd"`
	Currency         string            `json:"currency"`
	PricingWarning   *pricingWarning   `json:"pricing_warning,omitempty"`
	DataWarning      *DataWarning      `json:"data_warning,omitempty"`
	// Measurement is set when ingestion has stopped, so a low or zero
	// figure is not mistaken for a measurement of low or zero spend.
	Measurement *MeasurementWarning `json:"measurement,omitempty"`
}

// consumerEntry is one grouped spender row in tokenops_top_consumers.
type consumerEntry struct {
	Key      string  `json:"key"`
	Requests int64   `json:"requests"`
	Tokens   int64   `json:"tokens"`
	CostUSD  float64 `json:"cost_usd"`
	// APIEquivalentUSD is what the group would have billed at API list
	// prices. On a flat-rate plan CostUSD is $0 for every group, so this
	// is the only dollar figure that tells the groups apart — and the one
	// the list is ranked on.
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
}

// topConsumersResult is the typed payload for tokenops_top_consumers.
type topConsumersResult struct {
	// By is the grouping actually applied, "model" when the caller
	// omitted it — not an echo of the input, which was "" in that case.
	By       string          `json:"by"`
	Top      []consumerEntry `json:"top"`
	Currency string          `json:"currency"`
}

// forecastResult is the typed payload for tokenops_forecast. Note is set
// only when history is too short to project.
type forecastResult struct {
	HorizonDays   int                   `json:"horizon_days,omitempty"`
	HistoryPoints int                   `json:"history_points"`
	Forecast      []forecast.Prediction `json:"forecast"`
	// ForecastTokens projects token volume over the same horizon. On a
	// flat-rate plan the dollar forecast is a flat zero, so this is the
	// only series carrying signal — always returned alongside.
	ForecastTokens []forecast.Prediction `json:"forecast_tokens,omitempty"`
	Currency       string                `json:"currency,omitempty"`
	Note           string                `json:"note,omitempty"`
}

// workflowTraceResult is the typed payload for tokenops_workflow_trace.
type workflowTraceResult struct {
	Trace    *workflow.Trace              `json:"trace"`
	Findings []*eventschema.CoachingEvent `json:"findings"`
}

// optimizationEntry is one recommendation row in tokenops_optimizations.
type optimizationEntry struct {
	Timestamp              time.Time `json:"timestamp"`
	Kind                   string    `json:"kind"`
	Mode                   string    `json:"mode"`
	Decision               string    `json:"decision"`
	EstimatedSavingsTokens int64     `json:"estimated_savings_tokens"`
	EstimatedSavingsUSD    float64   `json:"estimated_savings_usd"`
	QualityScore           float64   `json:"quality_score"`
	Reason                 string    `json:"reason"`
	WorkflowID             string    `json:"workflow_id,omitempty"`
	AgentID                string    `json:"agent_id,omitempty"`
}

// optimizationsResult is the typed payload for tokenops_optimizations.
type optimizationsResult struct {
	Optimizations []optimizationEntry `json:"optimizations"`
	Currency      string              `json:"currency"`
}

// RegisterTools attaches the canonical TokenOps MCP tool surface (spend
// summary, top consumers, burn rate, forecast, workflow trace,
// optimizations) to s.
func RegisterTools(s *Server, d Deps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	if d.Store == nil || d.Aggregator == nil {
		return errors.New("mcp: deps require store + aggregator")
	}

	s.Tool("tokenops_spend_summary").
		Description("Return total requests, tokens, and cost over an optional time window. Use to answer 'how much did we spend last week?'").
		OutputSchema(spendSummaryResult{}).
		Handler(func(ctx context.Context, in spendSummaryInput) (*spendSummaryResult, error) {
			return spendSummary(ctx, d, in)
		})

	s.Tool("tokenops_top_consumers").
		Description("List top N consumers grouped by model, provider, workflow, or agent (default by=model, top=5), ranked by API-equivalent value — what the traffic would bill at list prices — then by tokens. On a flat-rate plan real cost_usd is $0 for every group, so api_equivalent_usd is the figure that ranks them.").
		OutputSchema(topConsumersResult{}).
		Handler(func(ctx context.Context, in topConsumersInput) (*topConsumersResult, error) {
			return topConsumers(ctx, d, in)
		})

	s.Tool("tokenops_burn_rate").
		Description("Return the burn rate over the last N hours (default 24): cost, tokens, and API-equivalent value, with the hourly series. On a flat-rate plan cost is $0 at the margin, so tokens are the burn.").
		Handler(func(ctx context.Context, in burnRateInput) (string, error) {
			return burnRate(ctx, d, in)
		})

	s.Tool("tokenops_forecast").
		Description("Forecast daily spend horizon_days into the future using Holt's exponential smoothing.").
		OutputSchema(forecastResult{}).
		Handler(func(ctx context.Context, in forecastInput) (*forecastResult, error) {
			return forecastSpend(ctx, d, in)
		})

	s.Tool("tokenops_workflow_trace").
		Description("Reconstruct a workflow trace and run the waste detector. Returns step-level deltas plus coaching findings.").
		OutputSchema(workflowTraceResult{}).
		Handler(func(ctx context.Context, in workflowTraceInput) (*workflowTraceResult, error) {
			return workflowTrace(ctx, d, in)
		})

	s.Tool("tokenops_optimizations").
		Description("List optimization recommendations recorded in the local event store. Mirrors `GET /api/optimizations`. Filter by workflow_id / agent_id / time window.").
		OutputSchema(optimizationsResult{}).
		Handler(func(ctx context.Context, in optimizationsInput) (*optimizationsResult, error) {
			return optimizations(ctx, d, in)
		})
	return nil
}

// --- handlers -------------------------------------------------------------

func (in spendSummaryInput) toFilter() (analytics.Filter, error) {
	f := analytics.Filter{
		WorkflowID: in.WorkflowID,
		AgentID:    in.AgentID,
	}
	if in.Since != "" {
		t, err := parseTimeOrDuration(in.Since)
		if err != nil {
			return f, fmt.Errorf("since: %w", err)
		}
		f.Since = t
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return f, fmt.Errorf("until: %w", err)
		}
		f.Until = t
	}
	f.IncludeSources = resolveIncludeSources(in.IncludeSources, in.IncludeDemo)
	return f, nil
}

func spendSummary(ctx context.Context, d Deps, in spendSummaryInput) (*spendSummaryResult, error) {
	filter, err := in.toFilter()
	if err != nil {
		return nil, err
	}
	summary, err := d.Aggregator.Summarize(ctx, filter)
	if err != nil {
		return nil, err
	}
	res := &spendSummaryResult{
		Window:           in,
		Requests:         summary.Requests,
		InputTokens:      summary.InputTokens,
		OutputTokens:     summary.OutputTokens,
		TotalTokens:      summary.TotalTokens,
		CostUSD:          summary.CostUSD,
		APIEquivalentUSD: summary.APIEquivalentUSD,
		Currency:         d.Spend.Currency(),
	}
	res.Measurement = measurementQuality(d)
	if len(summary.Unpriced) > 0 {
		models := make([]unpricedModel, 0, len(summary.Unpriced))
		for _, u := range summary.Unpriced {
			models = append(models, unpricedModel{
				Provider: u.Provider,
				Model:    u.Model,
				Requests: u.Requests,
			})
		}
		res.PricingWarning = &pricingWarning{
			Message:        "no rate in the pricing table for these models; cost_usd is underestimated",
			UnpricedModels: models,
		}
	}
	if !demoOptedIn(in.IncludeSources, in.IncludeDemo) {
		warn, werr := maybeDataWarning(ctx, d.Store, filter.Since, filter.Until)
		if werr == nil && warn != nil {
			res.DataWarning = warn
		}
	}
	return res, nil
}

// consumerGroups maps the accepted "by" values to their aggregation. The
// schema enum already refuses anything else at the MCP boundary; the
// handler enforces the same set so it does not depend on every caller
// arriving through that boundary.
var consumerGroups = map[string]analytics.Group{
	"model":    analytics.GroupModel,
	"provider": analytics.GroupProvider,
	"workflow": analytics.GroupWorkflow,
	"agent":    analytics.GroupAgent,
}

func topConsumers(ctx context.Context, d Deps, in topConsumersInput) (*topConsumersResult, error) {
	by := strings.ToLower(strings.TrimSpace(in.By))
	if by == "" {
		by = "model"
	}
	group, ok := consumerGroups[by]
	if !ok {
		// The old switch fell through to model here, answering a typo
		// with a confident ranking of something nobody asked about.
		return nil, fmt.Errorf("by: unknown grouping %q (want model, provider, workflow, or agent)", in.By)
	}
	f := analytics.Filter{}
	if in.Since != "" {
		t, err := parseTimeOrDuration(in.Since)
		if err != nil {
			return nil, err
		}
		f.Since = t
	} else {
		f.Since = time.Now().Add(-7 * 24 * time.Hour)
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return nil, err
		}
		f.Until = t
	}
	f.IncludeSources = resolveIncludeSources(in.IncludeSources, in.IncludeDemo)
	rows, err := d.Aggregator.AggregateBy(ctx, f, analytics.BucketDay, group)
	if err != nil {
		return nil, err
	}
	out := rankConsumers(rows)
	top := in.Top
	if top <= 0 {
		top = 5
	}
	if top < len(out) {
		out = out[:top]
	}
	return &topConsumersResult{By: by, Top: out, Currency: d.Spend.Currency()}, nil
}

// rankConsumers folds per-bucket rows into one entry per group key and
// orders them the way `tokenops spend` does.
//
// Ranked on the API-equivalent, not the real cost. On a flat-rate plan
// every plan-covered group costs $0, so a cost-keyed ranking put the one
// metered call first and left the models that carried the work in map
// order behind it. Tokens break ties (unpriced models are $0 on both
// figures), then the key, so the order is the same on every call.
func rankConsumers(rows []analytics.Row) []consumerEntry {
	byKey := map[string]*consumerEntry{}
	for _, r := range rows {
		e, ok := byKey[r.GroupKey]
		if !ok {
			e = &consumerEntry{Key: r.GroupKey}
			byKey[r.GroupKey] = e
		}
		e.Requests += r.Requests
		e.Tokens += r.TotalTokens
		e.CostUSD += r.CostUSD
		e.APIEquivalentUSD += r.APIEquivalentUSD
	}
	out := make([]consumerEntry, 0, len(byKey))
	for _, e := range byKey {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.APIEquivalentUSD != b.APIEquivalentUSD {
			return a.APIEquivalentUSD > b.APIEquivalentUSD
		}
		if a.Tokens != b.Tokens {
			return a.Tokens > b.Tokens
		}
		return a.Key < b.Key
	})
	return out
}

func burnRate(ctx context.Context, d Deps, in burnRateInput) (string, error) {
	hours := in.Hours
	if hours <= 0 {
		hours = 24
	}
	f := analytics.Filter{Since: time.Now().Add(-time.Duration(hours) * time.Hour)}
	f.IncludeSources = resolveIncludeSources(in.IncludeSources, in.IncludeDemo)
	rows, err := d.Aggregator.AggregateBy(ctx, f, analytics.BucketHour, analytics.GroupNone)
	if err != nil {
		return "", err
	}
	b := burnTotals{Hours: hours, Currency: d.Spend.Currency()}
	for _, r := range rows {
		b.Cost += r.CostUSD
		b.Tokens += r.TotalTokens
		b.APIEquivalent += r.APIEquivalentUSD
		b.CostSeries = append(b.CostSeries, r.CostUSD)
		b.TokenSeries = append(b.TokenSeries, float64(r.TotalTokens))
	}
	payload := map[string]any{
		"hours":  hours,
		"cost":   b.Cost,
		"tokens": b.Tokens,
		// The list-price value of the window. On a flat-rate plan cost
		// is structurally zero, and this is the dollar figure that still
		// moves with the work.
		"api_equivalent_usd": b.APIEquivalent,
		"hourly":             rows,
		"currency":           b.Currency,
	}
	if q := measurementQuality(d); q != nil {
		payload["measurement"] = q
	}
	return markdownPayload(renderBurnSummary(b), payload), nil
}

func forecastSpend(ctx context.Context, d Deps, in forecastInput) (*forecastResult, error) {
	horizon := in.HorizonDays
	if horizon <= 0 {
		horizon = 7
	}
	f := analytics.Filter{Since: time.Now().Add(-30 * 24 * time.Hour)}
	f.IncludeSources = resolveIncludeSources(in.IncludeSources, in.IncludeDemo)
	rows, err := d.Aggregator.AggregateBy(ctx, f, analytics.BucketDay, analytics.GroupNone)
	if err != nil {
		return nil, err
	}
	history := forecast.SeriesFromRows(rows, forecast.CostUSD)
	if len(history) < 2 {
		return &forecastResult{
			HistoryPoints: len(history),
			Forecast:      []forecast.Prediction{},
			Note:          "insufficient history (need ≥2 daily buckets)",
		}, nil
	}
	preds := forecast.AutoForecast(history, horizon, 24*time.Hour)
	tokenHistory := forecast.SeriesFromRows(rows, forecast.TotalTokens)
	res := &forecastResult{
		HorizonDays:    horizon,
		HistoryPoints:  len(history),
		Forecast:       preds,
		ForecastTokens: forecast.AutoForecast(tokenHistory, horizon, 24*time.Hour),
		Currency:       d.Spend.Currency(),
	}
	if allZeroForecast(preds) {
		// A flat-rate cost history is zero at every point, so the
		// projection is a flat zero with zero-width bands. Returned bare
		// that reads as a broken forecaster rather than the correct
		// answer to a question that does not apply.
		res.Note = "cost history is all zero (plan-covered traffic bills $0 at the margin), so the dollar forecast is flat zero — forecast_tokens carries the signal"
	}
	return res, nil
}

// allZeroForecast reports whether a projection is entirely zero. The CLI's
// `tokenops spend --forecast` suppresses the same case.
func allZeroForecast(points []forecast.Prediction) bool {
	for _, p := range points {
		if p.Value != 0 || p.Lower != 0 || p.Upper != 0 {
			return false
		}
	}
	return true
}

func workflowTrace(ctx context.Context, d Deps, in workflowTraceInput) (*workflowTraceResult, error) {
	if in.WorkflowID == "" {
		return nil, errors.New("workflow_id is required")
	}
	trace, err := workflow.Reconstruct(ctx, d.Store, d.Spend, in.WorkflowID)
	if err != nil {
		return nil, err
	}
	coachings := waste.New(d.wasteConfig()).Detect(trace)
	return &workflowTraceResult{
		Trace:    trace,
		Findings: coachings,
	}, nil
}

func optimizations(ctx context.Context, d Deps, in optimizationsInput) (*optimizationsResult, error) {
	f := sqlite.Filter{
		Type:       eventschema.EventTypeOptimization,
		WorkflowID: in.WorkflowID,
		AgentID:    in.AgentID,
		Limit:      in.Limit,
	}
	if in.Since != "" {
		t, err := parseTimeOrDuration(in.Since)
		if err != nil {
			return nil, fmt.Errorf("since: %w", err)
		}
		f.Since = t
	} else {
		f.Since = time.Now().Add(-7 * 24 * time.Hour)
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return nil, fmt.Errorf("until: %w", err)
		}
		f.Until = t
	}
	envs, err := d.Store.Query(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]optimizationEntry, 0, len(envs))
	for _, env := range envs {
		oe, ok := env.Payload.(*eventschema.OptimizationEvent)
		if !ok {
			continue
		}
		out = append(out, optimizationEntry{
			Timestamp:              env.Timestamp,
			Kind:                   string(oe.Kind),
			Mode:                   string(oe.Mode),
			Decision:               string(oe.Decision),
			EstimatedSavingsTokens: oe.EstimatedSavingsTokens,
			EstimatedSavingsUSD:    oe.EstimatedSavingsUSD,
			QualityScore:           oe.QualityScore,
			Reason:                 oe.Reason,
			WorkflowID:             oe.WorkflowID,
			AgentID:                oe.AgentID,
		})
	}
	return &optimizationsResult{
		Optimizations: out,
		Currency:      d.Spend.Currency(),
	}, nil
}

// --- helpers --------------------------------------------------------------

func parseTimeOrDuration(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil && days > 0 {
			return time.Now().Add(-time.Duration(days) * 24 * time.Hour), nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().Add(-d), nil
}

// wasteConfig is the waste detector config in effect for this call.
func (d Deps) wasteConfig() waste.Config {
	if d.WasteConfig != nil {
		return d.WasteConfig()
	}
	return d.Waste
}
