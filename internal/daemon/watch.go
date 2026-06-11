package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/config"
	"github.com/felixgeelhaar/tokenops/internal/contexts/governance/budget"
	"github.com/felixgeelhaar/tokenops/internal/contexts/observability/analytics"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/forecast"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
)

// runSpendWatcher is the active-mode background loop: every
// watch.interval it evaluates the configured budgets against the event
// store (actual spend in the calendar window + forecast for the
// remainder) and flags models the pricing table can't cost. Findings
// surface as structured log lines; budget breaches additionally publish
// BudgetExceeded domain events via the budget engine, so the dashboard
// event counters and audit trail pick them up.
//
// The watcher is read-only — it never mutates events and never blocks
// the request path.
func runSpendWatcher(ctx context.Context, cfg config.Config, agg *analytics.Aggregator, spendEng *spend.Engine, logger *slog.Logger) {
	interval := cfg.Watch.EffectiveInterval()
	limits := cfg.BudgetLimits()
	logger.Info("active mode: spend watcher running",
		"interval", interval,
		"budgets", len(limits),
	)

	// seen dedupes repeat findings so a 15m cadence doesn't re-log the
	// same alert until it escalates or the window rolls over.
	seen := map[string]bool{}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		watchTick(ctx, agg, spendEng, limits, seen, logger)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func watchTick(ctx context.Context, agg *analytics.Aggregator, spendEng *spend.Engine, limits []budget.Limit, seen map[string]bool, logger *slog.Logger) {
	now := time.Now().UTC()

	alerts := budget.EvaluateAll(limits,
		func(l budget.Limit) float64 {
			start := budget.WindowStart(l.Window, now)
			s, err := agg.Summarize(ctx, analytics.Filter{
				Since: start, Until: now,
				WorkflowID: l.WorkflowID, AgentID: l.AgentID,
			})
			if err != nil {
				logger.Warn("spend watcher: summarize failed", "budget", l.Name, "err", err)
				return 0
			}
			if l.Basis == budget.BasisEquivalent {
				return s.APIEquivalentUSD
			}
			return s.CostUSD
		},
		func(l budget.Limit) []forecast.Prediction {
			if l.Basis == budget.BasisEquivalent {
				// Daily cost rows carry real spend only; an equivalent-
				// basis forecast would need a parallel recomputed series.
				// Threshold alerts still fire — forecast is skipped.
				return nil
			}
			start := budget.WindowStart(l.Window, now)
			end := budget.WindowEnd(l.Window, start)
			daysLeft := int(end.Sub(now).Hours()/24) + 1
			rows, err := agg.AggregateBy(ctx, analytics.Filter{
				// Forecast trains on the trailing 14 days regardless of
				// the budget window so short windows still get a trend.
				Since:      now.AddDate(0, 0, -14),
				WorkflowID: l.WorkflowID, AgentID: l.AgentID,
			}, analytics.BucketDay, analytics.GroupNone)
			if err != nil {
				return nil
			}
			history := forecast.SeriesFromRows(rows, forecast.CostUSD)
			return forecast.AutoForecast(history, daysLeft, 24*time.Hour)
		},
	)
	for _, a := range alerts {
		key := fmt.Sprintf("%s|%s|%s|%s",
			a.Limit.Name, a.Kind, a.Severity,
			budget.WindowStart(a.Limit.Window, now).Format(time.RFC3339))
		if seen[key] {
			continue
		}
		seen[key] = true
		logger.Warn("budget alert",
			"budget", a.Limit.Name,
			"kind", string(a.Kind),
			"severity", a.Severity.String(),
			"message", a.Message,
		)
	}

	// Unpriced models in the last 24h — silent cost blind spots.
	s, err := agg.Summarize(ctx, analytics.Filter{Since: now.Add(-24 * time.Hour)})
	if err != nil {
		return
	}
	for _, u := range s.Unpriced {
		key := "unpriced|" + u.Provider + "|" + u.Model
		if seen[key] {
			continue
		}
		seen[key] = true
		logger.Warn("unpriced model: spend figures are underestimated",
			"provider", u.Provider,
			"model", u.Model,
			"requests", u.Requests,
			"hint", "add a rate via pricing.path or upgrade tokenops",
			"currency", spendEng.Currency(),
		)
	}
}
