package daemon

import (
	"log/slog"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// openRoutingApprovals opens the shared approval log. The daemon writes
// proposals to it and the MCP server reads them, so it lives on disk
// rather than in either process.
func openRoutingApprovals() (*routingapproval.Store, error) {
	path, err := routingapproval.DefaultPath()
	if err != nil {
		return nil, err
	}
	return routingapproval.Open(path)
}

// attachApprovalGate wires the preferred-model ceiling into a router
// config: which model is preferred, what the operator has already
// decided, and where a fresh proposal goes.
//
// Decisions are re-read per request rather than cached. The operator
// answers in a different process, and an answer that only took effect
// after a daemon restart would be worse than useless.
func attachApprovalGate(
	rc *router.Config,
	cfg config.Config,
	store *routingapproval.Store,
	logger *slog.Logger,
) {
	rc.PreferredModel = cfg.PreferredModel

	rc.UpgradeDecision = func(provider eventschema.Provider, from, to string) router.Decision {
		state, err := store.Load()
		if err != nil {
			// Unreadable log: fall back to Pending, which refuses the
			// upgrade. Failing closed keeps an unreviewed model change
			// from reaching the operator's request.
			logger.Warn("routing approvals unreadable; treating upgrade as pending", "err", err)
			return router.DecisionPending
		}
		key := router.Proposal{Provider: provider, FromModel: from, ProposedModel: to}.Key()
		st, ok := state[key]
		if !ok || !st.Decided {
			return router.DecisionPending
		}
		switch st.Decision {
		case string(router.DecisionApproved):
			return router.DecisionApproved
		case string(router.DecisionDenied):
			return router.DecisionDenied
		default:
			return router.DecisionPending
		}
	}

	rc.OnProposal = func(p router.Proposal) {
		if err := store.Propose(routingapproval.Record{
			Key:       p.Key(),
			Provider:  string(p.Provider),
			From:      p.FromModel,
			To:        p.ProposedModel,
			Preferred: p.PreferredModel,
			DeltaUSD:  p.EstimatedDeltaUSD,
			Priced:    p.Priced,
			Reason:    p.Reason,
		}); err != nil {
			logger.Warn("could not record routing proposal", "err", err, "route", p.Key())
			return
		}
		logger.Info("model upgrade refused pending approval",
			"from", p.FromModel, "proposed", p.ProposedModel,
			"preferred", p.PreferredModel, "reason", p.Reason)
	}
}
