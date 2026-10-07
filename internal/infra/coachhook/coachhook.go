// Package coachhook is the coaching half of the usage-hooks family: a Claude
// Code Stop hook that tracks a session's *cumulative* API-equivalent cost and,
// as that spend crosses fractions of a per-session budget, fires graduated,
// latched nudges to reclaim context (/compact or a fresh session). Cache-read
// is the dominant, most reclaimable cost in a long Claude Code session — every
// turn re-bills the entire accumulated context at the cache-read rate — but the
// damage is done by *accumulation*, not by any single extreme turn: a session
// running thousands of flat turns at a few hundred thousand cache-read tokens
// each quietly compounds into thousands of dollars while no single turn ever
// looks alarming. Phase 1's flat per-turn threshold missed exactly that shape.
//
// The hook reads only the *tail* of the local transcript jsonl (never the whole
// multi-MB file, never anything off-machine), sums the full API-equivalent cost
// of the new turns since it last looked, keeps a tiny per-session counter in
// ~/.tokenops/coach-hook/, and latches each budget-fraction alert so it fires
// once. It is a pure coach: it never blocks, never forces the agent to keep
// going, and fails open on every error — a coach must never disrupt the session.
package coachhook

import (
	"os"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
)

// DefaultBudgetUSD is the shipping per-session budget. Real sessions that ran
// 7,000–9,300 turns at ~600k cache-read tokens/turn accrued ~$2,400 in
// API-equivalent spend without any single turn being extreme; a $50 budget
// surfaces that drift long before it compounds that far.
const DefaultBudgetUSD = 50.0

// fracEpsilon absorbs float rounding when comparing budget fractions to tier
// boundaries, so a fraction that lands exactly on a boundary still counts as
// having reached it.
const fracEpsilon = 1e-9

// DefaultTiers are the budget fractions at which the coach nudges before the
// budget is exhausted: half, three-quarters, and the full budget.
func DefaultTiers() []float64 { return []float64{0.50, 0.75, 1.00} }

// Evaluate is the coach's decision + side effects for one Stop event. dir is
// the state/ledger root (defaults to ~/.tokenops/coach-hook when empty). It
// loads session state, reads the tail of transcriptPath, sums the full
// API-equivalent cost of every turn newer than the dedup marker into the
// session's cumulative spend, and — if that spend has crossed a budget-fraction
// boundary not yet alerted — nudges at the single highest such boundary
// (latching it). now is injected for tests. It never returns an error: on any
// failure it returns a no-nudge Decision so the caller can fail open.
func Evaluate(dir, sessionID, transcriptPath string, cfg Config, now time.Time) Decision {
	dir = resolveDir(dir)
	_ = os.MkdirAll(dir, 0o755)

	st := loadSession(dir, sessionID)

	model, contextTokens, unpriced := accumulate(transcriptPath, &st, cfg, now)
	resolved := observeTip(&st, model, contextTokens)

	// A budget the operator set is theirs; the shipping default is not,
	// and the wording depends on which this is.
	budget := cfg.BudgetUSD
	configured := budget > 0 && budget != DefaultBudgetUSD
	if budget <= 0 {
		budget = DefaultBudgetUSD
	}
	frac := st.CumulativeUSD / budget

	dec := Decision{CumulativeUSD: st.CumulativeUSD, BudgetUSD: budget, UnpricedModel: unpriced}
	fired := 0.0
	switch {
	case cfg.Quota != nil:
		evaluateQuota(dir, &dec, &st, cfg, model, contextTokens, now)
	case cfg.FlatPlan:
		evaluateReadingLost(dir, &dec, &st, cfg, now)
	default:
		fired = highestBoundary(frac, st.MaxFiredFraction, cfg)
		if cfg.Verbosity == verbosityQuiet && fired < 1.0-fracEpsilon {
			// Quiet speaks about money only once the budget is spent.
			// Left unlatched, so a louder setting still hears it.
			fired = 0
		}
	}
	if cfg.Enabled && fired > 0 {
		reason, retry := cfg.hold(budgetKind(fired), &st, now)
		switch {
		case reason == "":
			dec.Nudge = true
			dec.FiredFraction = fired
			dec.Message = nudgeMessage(fired, st.CumulativeUSD, budget, configured)
			// Context is the number that actually constrains the session;
			// lead the dollar figure with it wherever it is known.
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
			// The floor defers rather than drops: leave the tier
			// unlatched so it speaks at the next Stop past the floor.
			// Latching here would silence the finding for the rest of the
			// session, which is a mute, not a rate limit.
			dec.Suppressed = reason
		default:
			// The cap drops. Latch it, so the coach does not re-evaluate
			// a boundary it will never be allowed to speak.
			dec.Suppressed = reason
			st.MaxFiredFraction = fired
		}
	}

	evaluateCompactTip(&dec, &st, cfg, contextTokens, now)

	// The budget tier is about the session in flight; the read-guard case
	// is a standing recommendation that will be just as true next turn.
	// So the tier speaks first and the case waits for a later Stop, and
	// at most one thing is said per Stop either way: two findings landing
	// in the same breath is the shape `coaching.quiet` exists to stop,
	// and not saying both at once is the cheapest defence against it.
	if cfg.Enabled && !dec.Nudge && cfg.Promotion != "" && !st.PromotionNudged && cfg.Verbosity != verbosityQuiet {
		reason, _ := cfg.hold("", &st, now)
		switch {
		case reason == "":
			dec.Nudge = true
			dec.Promotion = true
			dec.Message = cfg.Promotion
			// Latch only on speaking. A case held back by the rate limit
			// is still a case; latching it here would argue it never.
			st.PromotionNudged = true
			st.LastNudgeAt = now.UTC().Format(time.RFC3339Nano)
			st.Nudges++
		case dec.Suppressed == "":
			dec.Suppressed = reason
		}
	}

	dec.Resolved = resolved
	offerTip(&st, &dec, cfg, tipKind(dec), model, contextTokens)
	saveSession(dir, sessionID, st)
	appendLedger(dir, ledgerEvent{
		TS: now.UTC(), Session: sessionID,
		CumulativeUSD: st.CumulativeUSD, BudgetUSD: budget,
		Fraction: frac, TierFired: dec.FiredFraction, Model: model,
		Suppressed: dec.Suppressed, Promotion: dec.Promotion, Compact: dec.CompactTip,
		Unpriced:    dec.UnpricedModel,
		QuotaWindow: quotaLabel(cfg.Quota), QuotaUsedPct: quotaUsed(cfg.Quota), QuotaTier: dec.QuotaTier,
		ReadingLost: dec.ReadingLost,
	})
	return dec
}

// highestBoundary returns the single highest budget-fraction boundary the
// session has now reached (frac) that has not yet been alerted
// (> maxFired). Boundaries are the configured Tiers plus, when
// OverBudgetStep>0, 1+k*step for k=1,2,… up to frac. Returning only the
// highest means a Stop that jumps 40%→120% fires the 100% tier alone, never a
// burst of every crossed tier. Zero means nothing new to fire.
func highestBoundary(frac, maxFired float64, cfg Config) float64 {
	best := 0.0
	consider := func(b float64) {
		if b <= frac+fracEpsilon && b > maxFired+fracEpsilon && b > best {
			best = b
		}
	}
	for _, t := range cfg.Tiers {
		if t > 0 {
			consider(t)
		}
	}
	if cfg.OverBudgetStep > 0 {
		for k := 1; k <= 100_000; k++ {
			b := 1.0 + float64(k)*cfg.OverBudgetStep
			if b > frac+fracEpsilon {
				break
			}
			consider(b)
		}
	}
	return best
}
