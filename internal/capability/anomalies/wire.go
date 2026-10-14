package anomalies

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/workflowtrace"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/infra/followthrough"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// DepsFor wires a run to the event store through agg, the clients'
// session directories for repositories, the waste detector over store
// for a runaway session's workflow, the plans cfg binds, and the
// follow-through ledger under the home directory. A nil store leaves the
// waste detector out.
func DepsFor(cfg config.Config, agg *analytics.Aggregator, store *sqlite.Store, eng *spend.Engine) Deps {
	planned := map[string]bool{}
	for p := range cfg.Plans {
		if cfg.PlanCovers(p) {
			planned[p] = true
		}
	}
	d := Deps{
		Turns: func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
			return agg.SessionTurns(ctx, analytics.Filter{Since: since})
		},
		Charges: func(ctx context.Context, since, until time.Time) ([]analytics.Charge, error) {
			return agg.DailyCharges(ctx, analytics.Filter{Since: since, Until: until})
		},
		RepoOf:        SessionRepos(gitlog.NewRepos()),
		PlanProviders: planned,
	}
	if store != nil {
		wcfg := cfg.Coaching.WasteConfig()
		d.Waste = func(ctx context.Context, wf string) []string {
			det, err := workflowtrace.Find(ctx, store, eng, wf, wcfg)
			if err != nil {
				return nil
			}
			out := make([]string, 0, len(det.Findings))
			for _, f := range det.Findings {
				out = append(out, f.Summary)
			}
			return out
		}
	}
	if l, err := followthrough.Default(); err == nil {
		d.Ledger = l
	}
	return d
}
