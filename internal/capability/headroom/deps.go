package headroom

import (
	"os"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/observedlimits"
	"go.klarlabs.de/tokenops/internal/infra/planevidence"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
)

// LocalDeps is what every surface on this machine computes headroom from:
// cfg's plans, the event store r, the rate card's price, and the
// machine's own records: the signed-in accounts, the plans TokenOps bound
// on a client's word, the plan history and the observed limits.
//
// Each surface used to build Deps by hand, and they had drifted apart:
// `tokenops plan headroom` named no plan TokenOps had bound, `tokenops
// otel` read no plan history (so a plan switch could read as an early
// reset) and no observed limits, the MCP server's own fallback named no
// signed-in account, and the team upload used the catalog's limits where
// the glance used the observed ones. One builder keeps them reading the
// same inputs.
//
// A nil r stays a nil Reader (storage disabled) and a nil price counts
// only measured cost. The advice cache is left to a caller that lives
// long enough to keep one (the daemon).
func LocalDeps(cfg *config.Config, r Reader, price plans.Pricer) Deps {
	return Deps{
		Config:      cfg,
		Reader:      r,
		Price:       price,
		Accounts:    SignedInAccounts,
		PlanFollows: actions.RecentPlanFollows,
		PlanHistory: planhistory.LoadDefault,
		Observed:    observedlimits.Loader(),
	}
}

// SignedInAccounts names the account each provider's client on this
// machine is signed in with, read on every glance: signing in as someone
// else must show at once. Nil when the home directory is unknown.
func SignedInAccounts() map[string]string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return planevidence.Accounts(home)
}
