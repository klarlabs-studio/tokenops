package planhistory

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/planswitch"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// Switch is one plan change, as the CLI and the MCP tool both make it.
type Switch struct {
	Provider, Previous, Plan string
	From, Now                time.Time
	// DBPath is the event store; empty skips re-marking.
	DBPath string
	// Actor names who made the change in the audit log.
	Actor string
}

// Outcome is what a switch did. StoreErr is set when a backdated switch
// could not open the event store, so earlier usage was not re-marked;
// the history is still recorded.
type Outcome struct {
	planswitch.Result
	StoreErr error
}

// Record writes the switch to the default history and, for a backdated
// one, re-marks earlier billed usage in the event store and audits it.
func Record(ctx context.Context, s Switch) (Outcome, error) {
	file, err := Default()
	if err != nil {
		return Outcome{}, err
	}
	var (
		out       Outcome
		restamper planswitch.Restamper
		store     *sqlite.Store
	)
	if s.From.Before(s.Now) && s.Plan != "" && s.DBPath != "" {
		openCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		st, err := sqlite.Open(openCtx, s.DBPath, sqlite.Options{})
		if err != nil {
			out.StoreErr = err
		} else {
			store, restamper = st, st
			defer func() { _ = st.Close() }()
		}
	}
	res, err := planswitch.Record(ctx, file, restamper, s.Provider, s.Previous, s.Plan, s.From, s.Now)
	out.Result = res
	if err != nil {
		return out, err
	}
	if store != nil {
		actor := s.Actor
		if actor == "" {
			actor = "cli"
		}
		_, _ = audit.NewRecorder(store).Record(ctx, audit.Entry{
			Action: audit.ActionPlanChange, Actor: actor, Target: s.Provider,
			Details: map[string]any{
				"previous": s.Previous, "plan": s.Plan, "from": s.From.Format(time.RFC3339),
				"restamped": res.Restamped,
			},
		})
	}
	return out, nil
}
