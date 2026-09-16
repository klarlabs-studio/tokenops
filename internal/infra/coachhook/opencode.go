package coachhook

import (
	"os"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode"
)

// opencode is the one client that already knows what its turns cost. It
// records a per-message `cost` in its own store — 6380 of 49,138
// assistant messages here carry a nonzero one — so the coach uses that
// figure in preference to inferring one from a rate card.
//
// The remaining 42,758 report zero, and that zero is usually CORRECT
// rather than missing: a GitHub Copilot turn or one through opencode's
// own gateway is included in a subscription and costs nothing at the
// margin. But the coaching budget is about API-EQUIVALENT spend — what a
// session would cost at list price, which is the only way to see context
// drift compounding when the actual bill is flat. So a zero falls back to
// the rate card rather than being taken at face value, exactly as it does
// for Claude Code on a subscription.
//
// The order matters and only one direction is safe: vendor figure first
// because it is ground truth, card second because it is the
// counterfactual the budget is denominated in, and "unpriced" reported
// out loud when neither can answer.

// EvaluateOpencode accounts for one opencode session going idle.
//
// The whole session is recomputed rather than accumulated. opencode's
// store holds every turn already, so summing it is both simpler and
// immune to the double-counting that an incremental marker invites —
// session.idle can fire repeatedly for one session, and a hook that adds
// on every fire would inflate a session without bound.
func EvaluateOpencode(dir, dbPath, sessionID string, cfg Config, now time.Time) Decision {
	dir = resolveDir(dir)
	// Without this the state and ledger writes fail silently: nothing
	// latches, so the nudge fires again on every idle — and opencode goes
	// idle every time the operator stops typing.
	_ = os.MkdirAll(dir, 0o755)
	st := loadSession(dir, sessionID)

	budget := cfg.BudgetUSD
	configured := budget > 0 && budget != DefaultBudgetUSD
	if budget <= 0 {
		budget = DefaultBudgetUSD
	}

	total, model, contextTokens, unpriced := sumOpencodeSession(dbPath, sessionID, cfg, now)
	st.CumulativeUSD = total

	dec := Decision{CumulativeUSD: total, BudgetUSD: budget, UnpricedModel: unpriced}
	frac := total / budget
	fired := highestBoundary(frac, st.MaxFiredFraction, cfg)
	if cfg.Enabled && fired > 0 {
		reason, retry := cfg.Quiet.silence(st.Nudges, parseTime(st.LastNudgeAt), now)
		switch {
		case reason == "":
			dec.Nudge = true
			dec.FiredFraction = fired
			dec.Message = nudgeMessage(fired, total, budget, configured)
			if note := contextNote(contextTokens, model); note != "" {
				dec.Message = note + " " + dec.Message
			}
			dec.ContextTokens = contextTokens
			if w, ok := spend.ContextWindow(model); ok {
				dec.ContextWindow = w
			}
			st.MaxFiredFraction = fired
			st.LastNudgeAt = now.UTC().Format(time.RFC3339Nano)
			st.Nudges++
		case retry:
			dec.Suppressed = reason
		default:
			dec.Suppressed = reason
			st.MaxFiredFraction = fired
		}
	}

	saveSession(dir, sessionID, st)
	appendLedger(dir, ledgerEvent{
		TS: now.UTC(), Session: sessionID,
		CumulativeUSD: total, BudgetUSD: budget,
		Fraction: frac, TierFired: dec.FiredFraction, Model: model,
		Suppressed: dec.Suppressed, Unpriced: dec.UnpricedModel,
	})
	return dec
}

// sumOpencodeSession totals one session, preferring opencode's own cost
// and falling back to the rate card.
func sumOpencodeSession(
	dbPath, sessionID string,
	cfg Config,
	now time.Time,
) (total float64, model string, contextTokens int64, unpriced string) {
	_ = opencode.ReadSession(dbPath, sessionID, func(t opencode.Turn) error {
		model = t.Model
		// The window holds whatever the newest turn carried. opencode
		// keeps its figures disjoint — total is input + output +
		// reasoning + cache read + write, verified against 44,374 real
		// records with none unexplained — so InputTokens, which the
		// reader has already summed across the input side, is the
		// context without double counting.
		contextTokens = int64(t.InputTokens)

		if t.Cost > 0 {
			total += t.Cost
			return nil
		}
		cost, ok := opencodeCardCost(cfg.ratesAt(now), t)
		if !ok {
			unpriced = t.Model
			return nil
		}
		total += cost
		return nil
	})
	return total, model, contextTokens, unpriced
}

// opencodeCardCost prices a turn opencode reported as free, so a
// subscription-covered session still shows the API-equivalent figure the
// budget is measured in.
//
// The reader hands over an input count that already folds in cache reads
// and writes, and an output count that already folds in reasoning. Only
// the cached share is separable, so it is billed at the cached rate and
// the remainder at the input rate — the same shape as Claude Code, whose
// figures opencode's also keep disjoint.
func opencodeCardCost(tbl spend.Table, t opencode.Turn) (float64, bool) {
	if t.Model == "" {
		return 0, false
	}
	r, err := tbl.Lookup(t.Provider, t.Model)
	if err != nil {
		// opencode names models without always naming a vendor the
		// catalog knows — "opencode/big-pickle" is its own gateway. Try
		// the name alone, and accept it only when one vendor claims it.
		var perr error
		r, _, perr = tbl.LookupAnyProvider(t.Model)
		if perr != nil {
			return 0, false
		}
	}
	cachedRate := r.CachedInputPerMillion
	if cachedRate == 0 {
		cachedRate = r.InputPerMillion
	}
	uncached := int64(t.InputTokens - t.CachedTokens)
	if uncached < 0 {
		uncached = 0
	}
	return perMillion(uncached, r.InputPerMillion) +
		perMillion(int64(t.CachedTokens), cachedRate) +
		perMillion(int64(t.OutputTokens), r.OutputPerMillion), true
}
