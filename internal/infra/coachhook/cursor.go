package coachhook

import (
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/infra/cursorturns"
)

// Cursor needs no transcript at all: its stop hook reports the turn's
// token counts in the payload itself. That makes it the cheapest of the
// three clients to support and the easiest to get wrong, because its
// cache accounting is a third distinct convention:
//
//	Claude Code  input_tokens and cache_read/cache_creation are DISJOINT
//	Codex        cached_input_tokens sits INSIDE input_tokens
//	Cursor       BOTH cache_read_tokens and cache_write_tokens sit inside
//	             input_tokens
//
// Cursor's own team states the third: "input_tokens is inclusive of
// cache_read_tokens and cache_write_tokens… only 14 of the 1.18M input
// tokens were genuinely uncached". Pricing a Cursor turn the Claude Code
// way would bill 1.18M tokens at the full input rate when 14 of them were
// uncached — an overstatement of roughly five orders of magnitude on the
// uncached line.

// cursorTurn is the token block a Cursor stop hook delivers. Every field
// is optional: Cursor documents them as possibly absent, so a missing one
// means "not reported", never zero-cost.
type cursorTurn struct {
	ConversationID string `json:"conversation_id"`
	GenerationID   string `json:"generation_id"`
	Model          string `json:"model"`
	ModelID        string `json:"model_id"`
	Status         string `json:"status"`

	InputTokens      *int64 `json:"input_tokens"`
	OutputTokens     *int64 `json:"output_tokens"`
	CacheReadTokens  *int64 `json:"cache_read_tokens"`
	CacheWriteTokens *int64 `json:"cache_write_tokens"`
}

// reported is true when Cursor actually sent usage for this turn.
func (c cursorTurn) reported() bool { return c.InputTokens != nil || c.OutputTokens != nil }

// modelName prefers the structured id. `model` is the composer slug —
// "cursor-grok-4.6-high-fast" — which encodes effort and speed flags no
// rate card carries; `model_id` is "grok-4.6", which one might.
func (c cursorTurn) modelName() string {
	if m := strings.TrimSpace(c.ModelID); m != "" {
		return m
	}
	return strings.TrimSpace(c.Model)
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	if *p < 0 {
		return 0
	}
	return *p
}

// cursorTurnCostUSD prices one Cursor turn.
//
// Uncached input is what remains after both cache figures are removed,
// clamped at zero: a payload reporting more cache than input is not
// something to reason about, and crediting the operator for tokens they
// used is the wrong direction to be wrong in.
//
// The vendor is looked up rather than assumed. Cursor resells many
// vendors' models and names them without saying whose they are, so an
// ambiguous name prices as unknown instead of being attributed by guess.
func cursorTurnCostUSD(tbl spend.Table, c cursorTurn) (float64, bool) {
	model := c.modelName()
	if model == "" || !c.reported() {
		return 0, false
	}
	r, _, err := tbl.LookupAnyProvider(model)
	if err != nil {
		return 0, false
	}
	cachedRate := r.CachedInputPerMillion
	if cachedRate == 0 {
		cachedRate = r.InputPerMillion
	}
	read, write := deref(c.CacheReadTokens), deref(c.CacheWriteTokens)
	uncached := deref(c.InputTokens) - read - write
	if uncached < 0 {
		uncached = 0
	}
	return perMillion(uncached, r.InputPerMillion) +
		perMillion(read, cachedRate) +
		perMillion(write, r.InputPerMillion) +
		perMillion(deref(c.OutputTokens), r.OutputPerMillion), true
}

// cursorContextTokens is how full the window was on this turn. Cache
// figures are already inside the input count, so adding them would count
// the same tokens twice.
func cursorContextTokens(c cursorTurn) int64 { return deref(c.InputTokens) }

// evaluateCursor accounts for one Cursor stop event.
//
// Deduplicated on generation_id rather than on a timestamp. Cursor's
// numbers are cumulative for the turn and it reports identical values on
// `stop` and `afterAgentResponse` for the same generation — and a stop
// hook that returns a follow-up fires again for the same conversation.
// Summing any of those twice would inflate the session by a whole turn.
func evaluateCursor(dir string, c cursorTurn, cfg Config, now time.Time) Decision {
	st := loadSession(dir, c.ConversationID)

	budget := cfg.BudgetUSD
	configured := budget > 0 && budget != DefaultBudgetUSD
	if budget <= 0 {
		budget = DefaultBudgetUSD
	}

	// Record the turn for ingestion before deciding anything. Cursor
	// keeps no per-turn record of its own, so this payload is the only
	// place these numbers ever exist — losing them to an early return
	// would keep Cursor on quota-only spend forever.
	cursorturns.Append(cfg.TurnLedgerDir, cursorturns.Turn{
		TS: now.UTC(), ConversationID: c.ConversationID, GenerationID: c.GenerationID,
		Model: c.Model, ModelID: c.ModelID,
		InputTokens: c.InputTokens, OutputTokens: c.OutputTokens,
		CacheReadTokens: c.CacheReadTokens, CacheWriteTokens: c.CacheWriteTokens,
	})

	// Already counted this generation? Fall through to the tier check
	// without adding it again.
	alreadyCounted := c.GenerationID != "" && c.GenerationID == st.LastCountedTS

	var unpriced string
	if !alreadyCounted && c.reported() {
		cost, priced := cursorTurnCostUSD(cfg.ratesAt(now), c)
		st.CumulativeUSD += cost
		if !priced {
			unpriced = c.modelName()
		}
		st.LastCountedTS = c.GenerationID
	}

	dec := Decision{CumulativeUSD: st.CumulativeUSD, BudgetUSD: budget, UnpricedModel: unpriced}
	frac := st.CumulativeUSD / budget
	fired := highestBoundary(frac, st.MaxFiredFraction, cfg)
	if cfg.Enabled && fired > 0 {
		reason, retry := cfg.Quiet.silence(st.Nudges, parseTime(st.LastNudgeAt), now)
		switch {
		case reason == "":
			dec.Nudge = true
			dec.FiredFraction = fired
			dec.Message = nudgeMessage(fired, st.CumulativeUSD, budget, configured)
			dec.ContextTokens = cursorContextTokens(c)
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

	saveSession(dir, c.ConversationID, st)
	appendLedger(dir, ledgerEvent{
		TS: now.UTC(), Session: c.ConversationID,
		CumulativeUSD: st.CumulativeUSD, BudgetUSD: budget,
		Fraction: frac, TierFired: dec.FiredFraction, Model: c.modelName(),
		Suppressed: dec.Suppressed, Unpriced: dec.UnpricedModel,
	})
	return dec
}
