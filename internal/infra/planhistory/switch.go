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
	// Price and Currency are what the operator pays per month, when they
	// said.
	Price    float64
	Currency string
	// SpendLimitUSD is the spend limit from From on, for a plan billed at
	// API rates.
	SpendLimitUSD float64
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
	res, err := planswitch.Record(ctx, file, restamper, planswitch.Change{
		Provider: s.Provider, Previous: s.Previous, Plan: s.Plan, From: s.From, Now: s.Now,
		Price: s.Price, Currency: s.Currency, SpendLimitUSD: s.SpendLimitUSD,
	})
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
				"restamped": res.Restamped, "restamped_to": res.RestampedTo, "price": s.Price, "currency": s.Currency,
				"spend_limit_usd": s.SpendLimitUSD,
			},
		})
	}
	return out, nil
}

// CorrectSpendCoverage runs planswitch.CorrectSpendCoverage against the
// default history and audits each correction, so a change TokenOps made
// on its own is visible in `tokenops audit`.
func CorrectSpendCoverage(ctx context.Context, store *sqlite.Store, current map[string]string, now time.Time) ([]planswitch.Correction, error) {
	file, err := Default()
	if err != nil {
		return nil, err
	}
	h, err := file.Load()
	if err != nil {
		return nil, err
	}
	fixed, err := planswitch.CorrectSpendCoverage(ctx, h, store, current, now)
	for _, c := range fixed {
		_, _ = audit.NewRecorder(store).Record(ctx, audit.Entry{
			Action: audit.ActionCostCorrection, Actor: "tokenops", Target: c.Provider,
			Details: map[string]any{
				"plan": c.Plan, "from": c.From.Format(time.RFC3339), "to": c.To.Format(time.RFC3339),
				"restamped": c.Restamped, "restamped_to": "metered",
				"reason": "usage under a plan billed at API rates was recorded as covered at $0",
			},
		})
	}
	return fixed, err
}
