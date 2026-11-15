package coach

import (
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

// LeverResult is one agent setting the context power manages: where the
// agent compacts on its own.
type LeverResult struct {
	Client string `json:"client"`
	Key    string `json:"key,omitempty"`
	Value  int64  `json:"value,omitempty"`
	// Status is applied, active, reverted, yours (the operator's own value,
	// left alone), edited (changed since tokenops set it), unavailable, or
	// shadow (what a preview would write, not written).
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// ContextLevers sets and restores where agents compact on their own. The
// CLI and the MCP server pass the implementation that edits the agents'
// settings files.
type ContextLevers interface {
	Apply() ([]LeverResult, error)
	Revert() ([]LeverResult, error)
	Check() []LeverResult
	// Preview is what Apply would do, writing nothing: a setting it would
	// write reports as LeverShadow.
	Preview() ([]LeverResult, error)
}

// reconcileContext applies the levers when the context power is
// autonomous and restores them when it stops acting (ask or autonomous).
// Applying is idempotent, so every change to the coach re-checks them. A
// power raised to ask proposes the line at once rather than writing it;
// one lowered from autonomous to ask keeps what is set.
func reconcileContext(before, after Report, levers ContextLevers, l Ledger, now time.Time) []LeverResult {
	if levers == nil {
		return nil
	}
	acts := func(r Report) bool {
		e := r.Effective(config.PowerContext)
		return e == config.AutonomyAutonomous || e == config.AutonomyAsk
	}
	is := after.Effective(config.PowerContext) == config.AutonomyAutonomous
	switch {
	case after.Effective(config.PowerContext) == config.AutonomyAsk:
		if before.Effective(config.PowerContext) == config.AutonomyAsk {
			return nil
		}
		would, err := wouldSet(levers)
		if len(would) > 0 && !answeredOrPending(foldedProposals(l, now), describeSettings(would)) {
			propose(l, now, describeSettings(would))
		}
		return withError(would, err)
	case is:
		rs, err := levers.Apply()
		for _, r := range rs {
			if r.Status == "applied" {
				RecordMove(l, now, NewID(), "", config.PowerContext, r.Client, "", r.Key)
			}
		}
		return withError(rs, err)
	case acts(before) && !acts(after):
		rs, err := levers.Revert()
		return withError(rs, err)
	}
	return nil
}

func withError(rs []LeverResult, err error) []LeverResult {
	if err != nil {
		rs = append(rs, LeverResult{Client: "tokenops", Status: "error", Note: err.Error()})
	}
	return rs
}
