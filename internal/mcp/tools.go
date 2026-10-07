package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/spending"

	"go.klarlabs.de/tokenops/internal/capability/money"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
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

	// Money returns the rate into the operator's currency, and whether
	// there is one. Optional: nil reports in US dollars only.
	Money func(ctx context.Context) (money.Rate, bool)
}

// --- input structs --------------------------------------------------------

type spendSummaryInput struct {
	Since          string   `json:"since,omitempty" jsonschema:"description=RFC3339 timestamp or duration like '24h' or '7d'"`
	Until          string   `json:"until,omitempty" jsonschema:"description=RFC3339 timestamp"`
	WorkflowID     string   `json:"workflow_id,omitempty"`
	AgentID        string   `json:"agent_id,omitempty"`
	IncludeSources []string `json:"include_sources,omitempty" jsonschema:"description=re-admit excluded activity-proxy source: 'mcp-session'. Synthetic events are not supported."`
}

type topConsumersInput struct {
	By             string   `json:"by,omitempty" jsonschema:"enum=model,enum=provider,enum=workflow,enum=agent"`
	Top            int      `json:"top,omitempty" jsonschema:"minimum=1,maximum=50"`
	Since          string   `json:"since,omitempty"`
	Until          string   `json:"until,omitempty"`
	IncludeSources []string `json:"include_sources,omitempty"`
}

type costPerCommitInput struct {
	Since string `json:"since,omitempty" jsonschema:"description=RFC3339 timestamp or duration like '7d'; default 7d"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Most recent commits to list (default 20); the totals cover them all"`
}

// sinceOrDefault is since parsed, or def before now when it is empty.
func sinceOrDefault(since string, def time.Duration) (time.Time, error) {
	if since == "" {
		return time.Now().Add(-def), nil
	}
	return parseTimeOrDuration(since)
}

type burnRateInput struct {
	Hours          int      `json:"hours,omitempty" jsonschema:"minimum=1,maximum=168"`
	IncludeSources []string `json:"include_sources,omitempty"`
}

type forecastInput struct {
	HorizonDays    int      `json:"horizon_days,omitempty" jsonschema:"minimum=1,maximum=30"`
	IncludeSources []string `json:"include_sources,omitempty"`
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

// unpricedModel names a (provider, model) pair with no API rate in the
// pricing table.
type unpricedModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Requests int64  `json:"requests"`
}

// pricingWarning explains that API-equivalent value is incomplete and
// metered cost may omit usage for models without a rate.
type pricingWarning struct {
	Message        string          `json:"message"`
	UnpricedModels []unpricedModel `json:"unpriced_models"`
}

// spendSummaryResult is the typed payload for tokenops_spend.
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
	// Measurement is set when ingestion has stopped, so a low or zero
	// figure is not mistaken for a measurement of low or zero spend.
	Measurement *MeasurementWarning `json:"measurement,omitempty"`
	// Display is the cost and value in the operator's currency, with the
	// rate used, when it is not the US dollar. A converted figure moves
	// with the exchange rate even when usage does not.
	Display *money.Display `json:"display,omitempty"`
	// RateNote names that rate, ready to quote.
	RateNote string `json:"rate_note,omitempty"`
}

// consumerEntry is one grouped spender row in tokenops_spend (view=top).
// topConsumersResult is the spending capability's payload, shared with
// the daemon API (ADR 0010 §4).
type topConsumersResult = spending.TopConsumers

// forecastResult is the typed payload for tokenops_spend (view=forecast),
// the spending capability's answer (ADR 0010 §4).
type forecastResult = spending.Forecast

// workflowTraceResult is the typed payload for tokenops_records (view=workflow).
type workflowTraceResult struct {
	Trace    *workflow.Trace              `json:"trace"`
	Findings []*eventschema.CoachingEvent `json:"findings"`
}

// optimizationEntry is one recommendation row in tokenops_records (view=optimizations).
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

// optimizationsResult is the typed payload for tokenops_records (view=optimizations).
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

	s.Tool("tokenops_cost_per_commit").
		Description("What each of the operator's commits cost: the agent work that led to it, priced, and the work no commit followed.").
		OutputSchema(commits.Report{}).
		Handler(func(ctx context.Context, in costPerCommitInput) (*commits.Report, error) {
			since, err := sinceOrDefault(in.Since, 7*24*time.Hour)
			if err != nil {
				return nil, inputError(err)
			}
			r, err := commits.Compute(ctx, commits.Deps{
				Turns: func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
					return d.Aggregator.SessionTurns(ctx, analytics.Filter{Since: since})
				},
			}, since)
			if err != nil {
				return nil, err
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 20
			}
			r = r.Newest(limit)
			return &r, nil
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
			out, err := workflowTrace(ctx, d, in)
			if errors.Is(err, workflow.ErrNoTrace) {
				// An unknown workflow is the caller's to correct; a store
				// failure stays masked. review_work shares workflowTrace and
				// needs ErrNoTrace unwrapped, so the mark goes on here.
				return nil, inputError(err)
			}
			return out, err
		})

	s.Tool("tokenops_optimizations").
		Description("List optimization recommendations recorded in the local event store. Mirrors `GET /api/optimizations`. Filter by workflow_id / agent_id / time window.").
		OutputSchema(optimizationsResult{}).
		Handler(func(ctx context.Context, in optimizationsInput) (*optimizationsResult, error) {
			return optimizations(ctx, d, in)
		})
	if err := registerIntentTools(s, d); err != nil {
		return err
	}
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
			return f, inputError(fmt.Errorf("since: %w", err))
		}
		f.Since = t
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return f, inputError(fmt.Errorf("until: %w", err))
		}
		f.Until = t
	}
	f.IncludeSources = resolveIncludeSources(in.IncludeSources)
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
	if d.Money != nil {
		if r, ok := d.Money(ctx); ok {
			if res.Display = money.Show(r, res.CostUSD, res.APIEquivalentUSD); res.Display != nil {
				res.RateNote = r.Note()
			}
		}
	}
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
			Message:        "no API rate in the pricing table for these models; API-equivalent is incomplete, metered cost_usd may omit their usage, and plan-covered usage still costs $0 at the margin",
			UnpricedModels: models,
		}
	}
	return res, nil
}

// consumerGroups maps the accepted "by" values to their aggregation. The
// schema enum already refuses anything else at the MCP boundary; the
// handler enforces the same set so it does not depend on every caller
// arriving through that boundary.
func topConsumers(ctx context.Context, d Deps, in topConsumersInput) (*topConsumersResult, error) {
	q := spending.TopQuery{By: in.By, Top: in.Top, IncludeSources: in.IncludeSources}
	if in.Since != "" {
		t, err := parseTimeOrDuration(in.Since)
		if err != nil {
			return nil, inputError(err)
		}
		q.Since = t
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return nil, inputError(err)
		}
		q.Until = t
	}
	res, err := spending.Top(ctx, d.Aggregator, q, d.Spend.Currency(), time.Now())
	if errors.Is(err, spending.ErrUnknownGrouping) {
		return nil, inputError(err)
	}
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func burnRate(ctx context.Context, d Deps, in burnRateInput) (string, error) {
	burn, err := spending.BurnRate(ctx, d.Aggregator, in.Hours, in.IncludeSources, d.Spend.Currency(), time.Now())
	if err != nil {
		return "", err
	}
	b := burnTotals{Hours: burn.Hours, Currency: burn.Currency, Cost: burn.Cost, Tokens: burn.Tokens, APIEquivalent: burn.APIEquivalentUSD}
	for _, r := range burn.Hourly {
		b.CostSeries = append(b.CostSeries, r.CostUSD)
		b.TokenSeries = append(b.TokenSeries, float64(r.TotalTokens))
	}
	payload := map[string]any{
		"hours":              burn.Hours,
		"cost":               burn.Cost,
		"tokens":             burn.Tokens,
		"api_equivalent_usd": burn.APIEquivalentUSD,
		"hourly":             burn.Hourly,
		"currency":           burn.Currency,
	}
	if q := measurementQuality(d); q != nil {
		payload["measurement"] = q
	}
	return markdownPayload(renderBurnSummary(b), payload), nil
}

func forecastSpend(ctx context.Context, d Deps, in forecastInput) (*forecastResult, error) {
	res, err := spending.ForecastSpend(ctx, d.Aggregator, in.HorizonDays, in.IncludeSources, d.Spend.Currency(), time.Now())
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func workflowTrace(ctx context.Context, d Deps, in workflowTraceInput) (*workflowTraceResult, error) {
	if in.WorkflowID == "" {
		return nil, inputError(errors.New("workflow_id is required"))
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
			return nil, inputError(fmt.Errorf("since: %w", err))
		}
		f.Since = t
	} else {
		f.Since = time.Now().Add(-7 * 24 * time.Hour)
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return nil, inputError(fmt.Errorf("until: %w", err))
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
