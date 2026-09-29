package coachhook

import (
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
)

// TipWindow is how many later turns a tip waits for the operator to act on
// it before it counts as ignored.
const TipWindow = 3

// TipResolution is the outcome of an earlier tip (ADR 0006, decision 6).
type TipResolution struct {
	ID       string
	Kind     string
	Followed bool
	Evidence string
}

// openTip is a tip still waiting to be acted on. Its levers are /compact,
// a fresh session, or a cheaper model, so it remembers the context size
// and model it was given at.
type openTip struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Model   string `json:"model,omitempty"`
	Context int64  `json:"context,omitempty"`
	Turns   int    `json:"turns,omitempty"`
}

// budgetKind names a dollar-tier tip for the follow-through ledger.
func budgetKind(fired float64) string {
	if fired >= 1.0-fracEpsilon {
		return "budget_over"
	}
	return fmt.Sprintf("budget_%d", int(fired*100+0.5))
}

// quotaKind names a quota-tier tip.
func quotaKind(tier float64) string { return fmt.Sprintf("quota_%d", int(tier*100+0.5)) }

// quietable reports whether a tip kind may go quiet when it keeps being
// ignored. Tips about work about to stop never do: the budget spent, or a
// quota window at 90% or more.
func quietable(kind string) bool {
	switch kind {
	case "budget_50", "budget_75", "quota_50", "quota_75", compactKind:
		return true
	}
	return false
}

// hold decides whether a tip of kind must not be spoken now: the quiet
// policy first, then the follow-through record. A tip quieted by the
// record is dropped for this session or window rather than retried.
func (c Config) hold(kind string, st *sessionState, now time.Time) (reason string, retry bool) {
	if reason, retry := c.Quiet.silence(st.Nudges, parseTime(st.LastNudgeAt), now); reason != "" {
		return reason, retry
	}
	if c.Verbosity != verbosityVerbose && quietable(kind) && c.Quieted != nil && c.Quieted(kind) {
		return "ignored_before", false
	}
	return "", false
}

// observeTip settles the session's open tip against a new turn: context at
// half or less of what it was when the tip was given is a compaction, a
// different model is a switch, and TipWindow turns without either is
// ignoring it.
func observeTip(st *sessionState, model string, contextTokens int64) []TipResolution {
	defer func() { st.compaction, st.dropFrom = "", 0 }()
	o := st.OpenTip
	if o == nil || contextTokens <= 0 {
		return nil
	}
	r := TipResolution{ID: o.ID, Kind: o.Kind}
	switch {
	case o.Context > 0 && contextTokens*2 <= o.Context && automaticCompaction(st, model):
		// The client compacted on its own at its ceiling: the context
		// shrank, but because nothing was done until it had to.
		r.Evidence = "the client compacted automatically at its ceiling"
	case o.Context > 0 && contextTokens*2 <= o.Context:
		r.Followed = true
		r.Evidence = fmt.Sprintf("context went from %s to %s", formatTokens(o.Context), formatTokens(contextTokens))
	case o.Model != "" && model != "" && model != o.Model:
		r.Followed = true
		r.Evidence = "the session moved to " + model
	case o.Turns+1 >= TipWindow:
		r.Evidence = fmt.Sprintf("nothing changed in %d turns", TipWindow)
	default:
		o.Turns++
		return nil
	}
	st.OpenTip = nil
	return []TipResolution{r}
}

// offerTip opens the tip this Stop spoke. A tip still open is closed as
// ignored: the next tier arrived before anything changed.
func offerTip(st *sessionState, dec *Decision, cfg Config, kind, model string, contextTokens int64) {
	if cfg.NewID == nil || !dec.Nudge || dec.Promotion || kind == "" {
		return
	}
	if o := st.OpenTip; o != nil {
		dec.Resolved = append(dec.Resolved, TipResolution{ID: o.ID, Kind: o.Kind, Evidence: "a later tip arrived first"})
	}
	st.OpenTip = &openTip{ID: cfg.NewID(), Kind: kind, Model: model, Context: contextTokens}
	dec.OfferID, dec.TipKind = st.OpenTip.ID, kind
}

// compactKind names the live compact tip in the follow-through ledger.
const compactKind = "compact_now"

// CompactAfterTurns is how many turns in a row above CompactAtTokens
// without a compaction earn the tip: the same bar as the compact_earlier
// finding, so the live tip and the report agree on what a habit is.
const CompactAfterTurns = 20

// countContext advances the stretch counter with one turn's context. A
// drop to half or less of the previous turn is a compaction (or a fresh
// start) and opens a new window.
func (st *sessionState) countContext(ctx, limit int64) {
	if ctx <= 0 {
		return
	}
	if st.LastContext > 0 && ctx*2 <= st.LastContext {
		st.AboveTurns, st.CompactTipped = 0, false
		st.dropFrom = st.LastContext
	}
	st.LastContext = ctx
	if limit > 0 && ctx >= limit {
		st.AboveTurns++
	}
}

// evaluateCompactTip gives the compact_now tip once per compaction window,
// when nothing else was said this Stop. Quiet leaves it out: it is advice
// about cost and drift, not a warning that work is about to stop.
func evaluateCompactTip(dec *Decision, st *sessionState, cfg Config, contextTokens int64, now time.Time) {
	limit := cfg.CompactAtTokens
	if !cfg.Enabled || dec.Nudge || limit <= 0 || st.CompactTipped ||
		st.AboveTurns < CompactAfterTurns || cfg.Verbosity == verbosityQuiet {
		return
	}
	reason, retry := cfg.hold(compactKind, st, now)
	switch {
	case reason == "":
		dec.Nudge, dec.CompactTip = true, true
		dec.Message = compactMessage(st.AboveTurns, contextTokens, limit, cfg.Verbosity)
		dec.ContextTokens = contextTokens
		st.CompactTipped = true
		st.LastNudgeAt = now.UTC().Format(time.RFC3339Nano)
		st.Nudges++
	case retry:
		dec.Suppressed = reason
	default:
		dec.Suppressed = reason
		st.CompactTipped = true
	}
}

func compactMessage(turns int, ctx, limit int64, verbosity string) string {
	msg := fmt.Sprintf("tokenops: context has stayed above %s for %d turns without compacting (now %s). "+
		"Every turn re-reads all of it: /compact now, or start a fresh session for the next task.",
		formatTokens(limit), turns, formatTokens(ctx))
	if verbosity == verbosityVerbose {
		msg += fmt.Sprintf(" Compacting at %s keeps the turns after it near %s on average instead.",
			formatTokens(limit), formatTokens((limit/8+limit)/2))
	}
	return msg
}

// ceilingShare is how close to the model's window a drop must start to be
// taken for the client's own compaction when no boundary says so. Claude
// Code compacts on its own just short of the window: 952k to 999k of 1M
// across the maintainer's automatic compactions.
const ceilingShare = 0.95

// automaticCompaction reports whether this Stop's compaction was the
// client's own rather than the operator's. The boundary entry says so
// when the transcript tail still holds it; a long compaction summary can
// push it out, and then a drop that started at the window's ceiling is
// taken as automatic.
func automaticCompaction(st *sessionState, model string) bool {
	switch st.compaction {
	case "auto":
		return true
	case "manual":
		return false
	}
	w, ok := spend.ContextWindow(model)
	return ok && w > 0 && float64(st.dropFrom) >= ceilingShare*float64(w)
}
