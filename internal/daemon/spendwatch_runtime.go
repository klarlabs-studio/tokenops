package daemon

import (
	"context"
	"log/slog"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/capability/budgets"
	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// startSpendWatcherRuntime owns registration of the active-mode budget
// watcher. publish takes the budget.exceeded events; store backs the
// audit-log check that keeps a restart from recording one twice; bill is
// the providers' own monthly figures, nil when none are read.
func startSpendWatcherRuntime(cfg config.Config, source budgets.Source, bill budgets.Bill, publish budgets.Publisher, store *sqlite.Store, currency string, sup *lifecycle.Supervisor, logger *slog.Logger) {
	if !cfg.ActiveMode() || source == nil {
		return
	}
	w := &budgets.Watcher{Source: source, Limits: cfg.BudgetLimits(), Publish: publish, Bill: bill}
	if store != nil {
		w.Recorded = budgets.RecordedIn(store)
	}
	sup.Go("spend-watcher", func(taskCtx context.Context) error {
		runSpendWatcher(taskCtx, w, cfg.Watch.EffectiveInterval(), currency, logger)
		return nil
	})
}

// taggedBudgetSource is agg, able to measure a budget scoped to an
// allocation tag by the configured rules.
func taggedBudgetSource(cfg config.Config, agg allocationTurns) budgets.Source {
	return allocation.BudgetSource(agg, &allocation.TagMeter{Turns: agg, Rules: cfg.AllocationRules()})
}

// allocationTurns is the aggregator: a budget source that reads session
// turns.
type allocationTurns interface {
	budgets.Source
	commits.TurnReader
}
