package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/analytics"
	"github.com/felixgeelhaar/tokenops/internal/forecast"
	"github.com/felixgeelhaar/tokenops/internal/spend"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/internal/waste"
	"github.com/felixgeelhaar/tokenops/internal/workflow"
)

// Deps wires the engines the TokenOps MCP tools query against. Pass a
// shared *sqlite.Store and reusable engines so opening / closing
// happens at the daemon level.
type Deps struct {
	Store      *sqlite.Store
	Aggregator *analytics.Aggregator
	Spend      *spend.Engine
}

// RegisterTools attaches the canonical TokenOps MCP tool surface (spend
// summary, top consumers, burn rate, forecast, workflow trace) to s.
func RegisterTools(s *Server, d Deps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	if d.Store == nil || d.Aggregator == nil {
		return errors.New("mcp: deps require store + aggregator")
	}

	s.AddTool(&Tool{
		Name:        "tokenops_spend_summary",
		Description: "Return total requests, tokens, and cost over an optional time window. Use to answer 'how much did we spend last week?'",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"since":{"type":"string","description":"RFC3339 timestamp or duration like '24h', '7d'"},"until":{"type":"string","description":"RFC3339 timestamp"},"workflow_id":{"type":"string"},"agent_id":{"type":"string"}}}`),
		Handler:     spendSummaryHandler(d),
	})

	s.AddTool(&Tool{
		Name:        "tokenops_top_consumers",
		Description: "List top N spenders grouped by model, provider, workflow, or agent. Default group=model, top=5.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"by":{"type":"string","enum":["model","provider","workflow","agent"]},"top":{"type":"integer","minimum":1,"maximum":50},"since":{"type":"string"},"until":{"type":"string"}}}`),
		Handler:     topConsumersHandler(d),
	})

	s.AddTool(&Tool{
		Name:        "tokenops_burn_rate",
		Description: "Return the spend burn rate over the last N hours (default 24).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"hours":{"type":"integer","minimum":1,"maximum":168}}}`),
		Handler:     burnRateHandler(d),
	})

	s.AddTool(&Tool{
		Name:        "tokenops_forecast",
		Description: "Forecast daily spend horizon_days into the future using Holt's exponential smoothing.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"horizon_days":{"type":"integer","minimum":1,"maximum":30}}}`),
		Handler:     forecastHandler(d),
	})

	s.AddTool(&Tool{
		Name:        "tokenops_workflow_trace",
		Description: "Reconstruct a workflow trace and run the waste detector. Returns step-level deltas plus coaching findings.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"workflow_id":{"type":"string"}},"required":["workflow_id"]}`),
		Handler:     workflowTraceHandler(d),
	})
	return nil
}

// --- handlers ----------------------------------------------------------

type windowArgs struct {
	Since      string `json:"since"`
	Until      string `json:"until"`
	WorkflowID string `json:"workflow_id"`
	AgentID    string `json:"agent_id"`
}

func (w windowArgs) toFilter() (analytics.Filter, error) {
	f := analytics.Filter{
		WorkflowID: w.WorkflowID,
		AgentID:    w.AgentID,
	}
	if w.Since != "" {
		t, err := parseTimeOrDuration(w.Since)
		if err != nil {
			return f, fmt.Errorf("since: %w", err)
		}
		f.Since = t
	}
	if w.Until != "" {
		t, err := time.Parse(time.RFC3339, w.Until)
		if err != nil {
			return f, fmt.Errorf("until: %w", err)
		}
		f.Until = t
	}
	return f, nil
}

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

func spendSummaryHandler(d Deps) ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args windowArgs
		_ = json.Unmarshal(raw, &args)
		filter, err := args.toFilter()
		if err != nil {
			return "", err
		}
		summary, err := d.Aggregator.Summarize(ctx, filter)
		if err != nil {
			return "", err
		}
		out := map[string]any{
			"window":        args,
			"requests":      summary.Requests,
			"input_tokens":  summary.InputTokens,
			"output_tokens": summary.OutputTokens,
			"total_tokens":  summary.TotalTokens,
			"cost_usd":      summary.CostUSD,
			"currency":      d.Spend.Currency(),
		}
		return jsonString(out), nil
	}
}

type topArgs struct {
	By    string `json:"by"`
	Top   int    `json:"top"`
	Since string `json:"since"`
	Until string `json:"until"`
}

func topConsumersHandler(d Deps) ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args topArgs
		_ = json.Unmarshal(raw, &args)
		group := analytics.GroupModel
		switch strings.ToLower(args.By) {
		case "provider":
			group = analytics.GroupProvider
		case "workflow":
			group = analytics.GroupWorkflow
		case "agent":
			group = analytics.GroupAgent
		}
		f := analytics.Filter{}
		if args.Since != "" {
			t, err := parseTimeOrDuration(args.Since)
			if err != nil {
				return "", err
			}
			f.Since = t
		} else {
			f.Since = time.Now().Add(-7 * 24 * time.Hour)
		}
		if args.Until != "" {
			t, err := time.Parse(time.RFC3339, args.Until)
			if err != nil {
				return "", err
			}
			f.Until = t
		}
		rows, err := d.Aggregator.AggregateBy(ctx, f, analytics.BucketDay, group)
		if err != nil {
			return "", err
		}
		// Sum across buckets per group key.
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
		// Simple bubble sort by cost desc — list is short (≤ 50).
		for i := 0; i < len(out); i++ {
			for j := i + 1; j < len(out); j++ {
				if out[j].CostUSD > out[i].CostUSD {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
		top := args.Top
		if top <= 0 {
			top = 5
		}
		if top < len(out) {
			out = out[:top]
		}
		return jsonString(map[string]any{"by": args.By, "top": out, "currency": d.Spend.Currency()}), nil
	}
}

type burnArgs struct {
	Hours int `json:"hours"`
}

func burnRateHandler(d Deps) ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args burnArgs
		_ = json.Unmarshal(raw, &args)
		hours := args.Hours
		if hours <= 0 {
			hours = 24
		}
		f := analytics.Filter{Since: time.Now().Add(-time.Duration(hours) * time.Hour)}
		rows, err := d.Aggregator.AggregateBy(ctx, f, analytics.BucketHour, analytics.GroupNone)
		if err != nil {
			return "", err
		}
		var total float64
		for _, r := range rows {
			total += r.CostUSD
		}
		return jsonString(map[string]any{
			"hours":   hours,
			"cost":   total,
			"hourly": rows,
			"currency": d.Spend.Currency(),
		}), nil
	}
}

type forecastArgs struct {
	HorizonDays int `json:"horizon_days"`
}

func forecastHandler(d Deps) ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args forecastArgs
		_ = json.Unmarshal(raw, &args)
		horizon := args.HorizonDays
		if horizon <= 0 {
			horizon = 7
		}
		f := analytics.Filter{Since: time.Now().Add(-30 * 24 * time.Hour)}
		rows, err := d.Aggregator.AggregateBy(ctx, f, analytics.BucketDay, analytics.GroupNone)
		if err != nil {
			return "", err
		}
		history := forecast.SeriesFromRows(rows, forecast.CostUSD)
		var preds []forecast.Prediction
		switch {
		case len(history) >= 4:
			preds, err = forecast.NewHolt(0.6, 0.3).Forecast(history, horizon, 24*time.Hour)
		case len(history) >= 2:
			preds, err = forecast.NewLinear().Forecast(history, horizon, 24*time.Hour)
		default:
			return jsonString(map[string]any{
				"history_points": len(history),
				"forecast":       []forecast.Prediction{},
				"note":           "insufficient history (need ≥2 daily buckets)",
			}), nil
		}
		if err != nil {
			return "", err
		}
		return jsonString(map[string]any{
			"horizon_days":   horizon,
			"history_points": len(history),
			"forecast":       preds,
			"currency":       d.Spend.Currency(),
		}), nil
	}
}

type traceArgs struct {
	WorkflowID string `json:"workflow_id"`
}

func workflowTraceHandler(d Deps) ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args traceArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.WorkflowID == "" {
			return "", errors.New("workflow_id is required")
		}
		trace, err := workflow.Reconstruct(ctx, d.Store, d.Spend, args.WorkflowID)
		if err != nil {
			return "", err
		}
		coachings := waste.New(waste.Config{}).Detect(trace)
		return jsonString(map[string]any{
			"trace":    trace,
			"findings": coachings,
		}), nil
	}
}

// jsonString marshals v indented, returning a string suitable for the
// MCP "text" content block.
func jsonString(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return string(b)
}
