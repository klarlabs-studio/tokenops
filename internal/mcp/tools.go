package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/coaching/waste"
	"github.com/felixgeelhaar/tokenops/internal/contexts/observability/analytics"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/forecast"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/internal/contexts/workflows/workflow"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Deps wires the engines the TokenOps MCP tools query against. Pass a
// shared *sqlite.Store and reusable engines so opening / closing
// happens at the daemon level.
type Deps struct {
	Store      *sqlite.Store
	Aggregator *analytics.Aggregator
	Spend      *spend.Engine
}

// --- input structs --------------------------------------------------------

type spendSummaryInput struct {
	Since       string `json:"since,omitempty" jsonschema:"description=RFC3339 timestamp or duration like '24h' or '7d'"`
	Until       string `json:"until,omitempty" jsonschema:"description=RFC3339 timestamp"`
	WorkflowID  string `json:"workflow_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	IncludeDemo bool   `json:"include_demo,omitempty" jsonschema:"description=include synthetic events seeded via tokenops demo (excluded by default)"`
}

type topConsumersInput struct {
	By          string `json:"by,omitempty" jsonschema:"enum=model,enum=provider,enum=workflow,enum=agent"`
	Top         int    `json:"top,omitempty" jsonschema:"minimum=1,maximum=50"`
	Since       string `json:"since,omitempty"`
	Until       string `json:"until,omitempty"`
	IncludeDemo bool   `json:"include_demo,omitempty"`
}

type burnRateInput struct {
	Hours       int  `json:"hours,omitempty" jsonschema:"minimum=1,maximum=168"`
	IncludeDemo bool `json:"include_demo,omitempty"`
}

type forecastInput struct {
	HorizonDays int  `json:"horizon_days,omitempty" jsonschema:"minimum=1,maximum=30"`
	IncludeDemo bool `json:"include_demo,omitempty"`
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
		Handler(func(ctx context.Context, in spendSummaryInput) (string, error) {
			return spendSummary(ctx, d, in)
		})

	s.Tool("tokenops_top_consumers").
		Description("List top N spenders grouped by model, provider, workflow, or agent. Default group=model, top=5.").
		Handler(func(ctx context.Context, in topConsumersInput) (string, error) {
			return topConsumers(ctx, d, in)
		})

	s.Tool("tokenops_burn_rate").
		Description("Return the spend burn rate over the last N hours (default 24).").
		Handler(func(ctx context.Context, in burnRateInput) (string, error) {
			return burnRate(ctx, d, in)
		})

	s.Tool("tokenops_forecast").
		Description("Forecast daily spend horizon_days into the future using Holt's exponential smoothing.").
		Handler(func(ctx context.Context, in forecastInput) (string, error) {
			return forecastSpend(ctx, d, in)
		})

	s.Tool("tokenops_workflow_trace").
		Description("Reconstruct a workflow trace and run the waste detector. Returns step-level deltas plus coaching findings.").
		Handler(func(ctx context.Context, in workflowTraceInput) (string, error) {
			return workflowTrace(ctx, d, in)
		})

	s.Tool("tokenops_optimizations").
		Description("List optimization recommendations recorded in the local event store. Mirrors `GET /api/optimizations`. Filter by workflow_id / agent_id / time window.").
		Handler(func(ctx context.Context, in optimizationsInput) (string, error) {
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
	if in.IncludeDemo {
		// Empty (non-nil) slice opts out of the default exclude list,
		// surfacing demo + replay sources alongside real traffic.
		f.ExcludeSources = []string{}
	}
	return f, nil
}

func spendSummary(ctx context.Context, d Deps, in spendSummaryInput) (string, error) {
	filter, err := in.toFilter()
	if err != nil {
		return "", err
	}
	summary, err := d.Aggregator.Summarize(ctx, filter)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"window":        in,
		"requests":      summary.Requests,
		"input_tokens":  summary.InputTokens,
		"output_tokens": summary.OutputTokens,
		"total_tokens":  summary.TotalTokens,
		"cost_usd":      summary.CostUSD,
		"currency":      d.Spend.Currency(),
	}
	if !in.IncludeDemo {
		warn, werr := maybeDataWarning(ctx, d.Store, filter.Since, filter.Until)
		if werr == nil && warn != nil {
			payload["data_warning"] = warn
		}
	}
	return jsonString(payload), nil
}

func topConsumers(ctx context.Context, d Deps, in topConsumersInput) (string, error) {
	group := analytics.GroupModel
	switch strings.ToLower(in.By) {
	case "provider":
		group = analytics.GroupProvider
	case "workflow":
		group = analytics.GroupWorkflow
	case "agent":
		group = analytics.GroupAgent
	}
	f := analytics.Filter{}
	if in.Since != "" {
		t, err := parseTimeOrDuration(in.Since)
		if err != nil {
			return "", err
		}
		f.Since = t
	} else {
		f.Since = time.Now().Add(-7 * 24 * time.Hour)
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return "", err
		}
		f.Until = t
	}
	if in.IncludeDemo {
		f.ExcludeSources = []string{}
	}
	rows, err := d.Aggregator.AggregateBy(ctx, f, analytics.BucketDay, group)
	if err != nil {
		return "", err
	}
	totals := map[string]float64{}
	tokens := map[string]int64{}
	reqs := map[string]int64{}
	for _, r := range rows {
		totals[r.GroupKey] += r.CostUSD
		tokens[r.GroupKey] += r.TotalTokens
		reqs[r.GroupKey] += r.Requests
	}
	type entry struct {
		Key      string  `json:"key"`
		Requests int64   `json:"requests"`
		Tokens   int64   `json:"tokens"`
		CostUSD  float64 `json:"cost_usd"`
	}
	out := make([]entry, 0, len(totals))
	for k, v := range totals {
		out = append(out, entry{Key: k, Requests: reqs[k], Tokens: tokens[k], CostUSD: v})
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CostUSD > out[i].CostUSD {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	top := in.Top
	if top <= 0 {
		top = 5
	}
	if top < len(out) {
		out = out[:top]
	}
	return jsonString(map[string]any{"by": in.By, "top": out, "currency": d.Spend.Currency()}), nil
}

func burnRate(ctx context.Context, d Deps, in burnRateInput) (string, error) {
	hours := in.Hours
	if hours <= 0 {
		hours = 24
	}
	f := analytics.Filter{Since: time.Now().Add(-time.Duration(hours) * time.Hour)}
	if in.IncludeDemo {
		f.ExcludeSources = []string{}
	}
	rows, err := d.Aggregator.AggregateBy(ctx, f, analytics.BucketHour, analytics.GroupNone)
	if err != nil {
		return "", err
	}
	var total float64
	series := make([]float64, 0, len(rows))
	for _, r := range rows {
		total += r.CostUSD
		series = append(series, r.CostUSD)
	}
	payload := map[string]any{
		"hours":    hours,
		"cost":     total,
		"hourly":   rows,
		"currency": d.Spend.Currency(),
	}
	return markdownPayload(renderBurnSummary(hours, total, d.Spend.Currency(), series), payload), nil
}

func forecastSpend(ctx context.Context, d Deps, in forecastInput) (string, error) {
	horizon := in.HorizonDays
	if horizon <= 0 {
		horizon = 7
	}
	f := analytics.Filter{Since: time.Now().Add(-30 * 24 * time.Hour)}
	if in.IncludeDemo {
		f.ExcludeSources = []string{}
	}
	rows, err := d.Aggregator.AggregateBy(ctx, f, analytics.BucketDay, analytics.GroupNone)
	if err != nil {
		return "", err
	}
	history := forecast.SeriesFromRows(rows, forecast.CostUSD)
	if len(history) < 2 {
		return jsonString(map[string]any{
			"history_points": len(history),
			"forecast":       []forecast.Prediction{},
			"note":           "insufficient history (need ≥2 daily buckets)",
		}), nil
	}
	preds := forecast.AutoForecast(history, horizon, 24*time.Hour)
	return jsonString(map[string]any{
		"horizon_days":   horizon,
		"history_points": len(history),
		"forecast":       preds,
		"currency":       d.Spend.Currency(),
	}), nil
}

func workflowTrace(ctx context.Context, d Deps, in workflowTraceInput) (string, error) {
	if in.WorkflowID == "" {
		return "", errors.New("workflow_id is required")
	}
	trace, err := workflow.Reconstruct(ctx, d.Store, d.Spend, in.WorkflowID)
	if err != nil {
		return "", err
	}
	coachings := waste.New(waste.Config{}).Detect(trace)
	return jsonString(map[string]any{
		"trace":    trace,
		"findings": coachings,
	}), nil
}

func optimizations(ctx context.Context, d Deps, in optimizationsInput) (string, error) {
	f := sqlite.Filter{
		Type:       eventschema.EventTypeOptimization,
		WorkflowID: in.WorkflowID,
		AgentID:    in.AgentID,
		Limit:      in.Limit,
	}
	if in.Since != "" {
		t, err := parseTimeOrDuration(in.Since)
		if err != nil {
			return "", fmt.Errorf("since: %w", err)
		}
		f.Since = t
	} else {
		f.Since = time.Now().Add(-7 * 24 * time.Hour)
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return "", fmt.Errorf("until: %w", err)
		}
		f.Until = t
	}
	envs, err := d.Store.Query(ctx, f)
	if err != nil {
		return "", err
	}
	type entry struct {
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
	out := make([]entry, 0, len(envs))
	for _, env := range envs {
		oe, ok := env.Payload.(*eventschema.OptimizationEvent)
		if !ok {
			continue
		}
		out = append(out, entry{
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
	return jsonString(map[string]any{
		"optimizations": out,
		"currency":      d.Spend.Currency(),
	}), nil
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
