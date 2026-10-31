package teamshare

import (
	"context"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// MachineDeps reads this machine for an upload: its transcripts, its
// event store through agg and store, the rate card in eng, and the
// vendors' bills with the admin keys keys returns. A nil store leaves the
// schema 2 sections out.
//
// Every part resolves directories through one git cache, kept as long as
// the Deps. Each part had its own, so the first upload after the daemon
// started resolved a month of session directories three times: about 30s
// each for the repositories, the waste report and the units of work, in a
// daemon macOS runs at background priority.
func MachineDeps(cfg config.Config, agg *analytics.Aggregator, store *sqlite.Store, eng *spend.Engine, keys func(context.Context) map[string]string) Deps {
	d, _, _ := machineDeps(cfg, agg, store, eng, keys)
	return d
}

// machineDeps is MachineDeps with the readers of the waste report and of
// the units of work, so a test can see what they share.
func machineDeps(cfg config.Config, agg *analytics.Aggregator, store *sqlite.Store, eng *spend.Engine,
	keys func(context.Context) map[string]string) (Deps, avoidable.Deps, uniteconomics.Deps) {
	repos := gitlog.NewRepos()
	d := DepsFor(agg)
	d.Repos = repos
	placed := uniteconomics.Deps{Turns: d.Turns, Dirs: d.Dirs, Repos: repos}
	d.Schema3 = Schema3{Placed: PlacedFor(placed), VendorCosts: VendorCostsFor(keys)}
	if store == nil {
		return d, avoidable.Deps{}, placed
	}
	waste := avoidable.DepsFor(cfg, agg, store, eng)
	waste.Dirs, waste.Repos = d.Dirs, repos
	d.FinOps = finOpsFor(cfg, store, eng, waste)
	return d, waste, placed
}
