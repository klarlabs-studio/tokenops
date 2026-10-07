package daemon

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/budgets"
)

// runSpendWatcher is the active-mode background loop: every
// watch.interval it asks the budgets capability what is new — budget
// alerts, and models the price list cannot cost — and logs it. A budget
// that reaches its limit also raises budget.exceeded once per window,
// which the audit log and the event counters pick up.
//
// The watcher is read-only — it never mutates events and never blocks
// the request path.
func runSpendWatcher(ctx context.Context, w *budgets.Watcher, interval time.Duration, currency string, logger *slog.Logger) {
	logger.Info("active mode: spend watcher running", "interval", interval, "budgets", len(w.Limits))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		logWatch(w.Tick(ctx, time.Now().UTC()), currency, logger)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// logWatch writes a tick's findings as structured log lines.
func logWatch(r budgets.Report, currency string, logger *slog.Logger) {
	for _, err := range r.Errors {
		logger.Warn("spend watcher: read failed", "err", err)
	}
	for _, a := range r.Alerts {
		logger.Warn("budget alert",
			"budget", a.Limit.Name,
			"kind", string(a.Kind),
			"severity", a.Severity.String(),
			"message", a.Message,
		)
	}
	for _, u := range r.Unpriced {
		logger.Warn("unpriced model: spend figures are underestimated",
			"provider", u.Provider,
			"model", u.Model,
			"requests", u.Requests,
			"hint", "add a rate via pricing.path or upgrade tokenops",
			"currency", currency,
		)
	}
}
