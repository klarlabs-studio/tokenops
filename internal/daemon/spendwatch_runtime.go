package daemon

import (
	"context"
	"log/slog"

	"go.klarlabs.de/tokenops/internal/capability/budgets"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// startSpendWatcherRuntime owns registration of the active-mode budget
// watcher. publish takes the budget.exceeded events; store backs the
// audit-log check that keeps a restart from recording one twice.
func startSpendWatcherRuntime(cfg config.Config, source budgets.Source, publish budgets.Publisher, store *sqlite.Store, currency string, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if !cfg.ActiveMode() || source == nil {
		return
	}
	w := &budgets.Watcher{Source: source, Limits: cfg.BudgetLimits(), Publish: publish}
	if store != nil {
		w.Recorded = budgets.RecordedIn(store)
	}
	sup.Go("spend-watcher", func(taskCtx context.Context) error {
		runSpendWatcher(taskCtx, w, cfg.Watch.EffectiveInterval(), currency, logger)
		return nil
	})
}
