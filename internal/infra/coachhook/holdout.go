package coachhook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/learning"
)

// SuppressedHoldout: the tip was due and held back for the session's
// holdout cohort, logged as shadow.
const SuppressedHoldout = "holdout"

// compactedShare is the share of a context a compaction leaves: Claude
// Code's summaries land near an eighth of what they replace, the same
// figure the compact tip's verbose wording uses.
const compactedShare = 8

// applyHoldout prices the tip this Stop is about to give and, when the
// session belongs to the holdout for the tip's family, holds it back as a
// shadow tip instead. Held tips keep their latches, so the coach does
// not reconsider them every turn, but do not count as having spoken.
// Tips about work about to stop are never eligible.
func (c Config) applyHoldout(dec *Decision, st *sessionState, before spoken, model string, contextTokens int64, now time.Time) {
	dec.ShadowResolved = append(dec.ShadowResolved, st.shadowResolved...)
	st.shadowResolved = nil
	if !dec.Nudge {
		return
	}
	kind := tipKind(*dec)
	if kind == "" {
		return
	}
	dec.ExpectedUSD, dec.ExpectedTokens = c.expectedSaving(model, contextTokens, now)
	dec.Evidence = tipEvidence(dec, model, contextTokens)
	if c.NewID == nil || !learning.InHoldout(st.id, kind, c.HoldoutPercent) {
		return
	}
	dec.Nudge, dec.Holdout, dec.Suppressed, dec.Message = false, true, SuppressedHoldout, ""
	st.Nudges, st.LastNudgeAt = before.nudges, before.last
	if o := st.ShadowTip; o != nil {
		dec.ShadowResolved = append(dec.ShadowResolved, TipResolution{ID: o.ID, Kind: o.Kind,
			Evidence: "a later tip arrived first", Signal: learning.SignalSuperseded})
	}
	st.ShadowTip = &openTip{ID: c.NewID(), Kind: kind, Model: model, Context: contextTokens}
	dec.ShadowID, dec.ShadowKind = st.ShadowTip.ID, kind
}

// recordShadow records, while the coach only observes, the budget tier it
// would have spoken: a shadow tip, settled like a spoken one, which is
// the evidence for letting it speak (#662). Each tier once a session.
func (c Config) recordShadow(dec *Decision, st *sessionState, frac float64, model string, contextTokens int64, now time.Time) {
	fired := highestBoundary(frac, st.ShadowFired, c)
	if fired <= 0 || c.NewID == nil {
		return
	}
	st.ShadowFired = fired
	kind := budgetKind(fired)
	if o := st.ShadowTip; o != nil {
		st.shadowResolved = append(st.shadowResolved, TipResolution{ID: o.ID, Kind: o.Kind,
			Evidence: "a later tip arrived first", Signal: learning.SignalSuperseded})
	}
	st.ShadowTip = &openTip{ID: c.NewID(), Kind: kind, Model: model, Context: contextTokens}
	dec.ShadowID, dec.ShadowKind = st.ShadowTip.ID, kind
	dec.ExpectedUSD, dec.ExpectedTokens = c.expectedSaving(model, contextTokens, now)
	dec.Evidence = tipEvidence(dec, model, contextTokens)
}

// expectedSaving is what compacting now would save on each later turn:
// the context it removes, re-read at the model's cache-read rate. Zero
// when the context or the model's price is unknown.
func (c Config) expectedSaving(model string, contextTokens int64, now time.Time) (float64, int64) {
	if contextTokens <= 0 {
		return 0, 0
	}
	tokens := contextTokens - contextTokens/compactedShare
	rate, _, err := c.ratesAt(now).LookupAnyProvider(model)
	if err != nil {
		return 0, tokens
	}
	return float64(tokens) * rate.CacheReadRate() / 1e6, tokens
}

// tipEvidence is the observation a tip rests on, in figures only.
func tipEvidence(dec *Decision, model string, contextTokens int64) string {
	ev := fmt.Sprintf("session $%.2f of a $%.2f budget", dec.CumulativeUSD, dec.BudgetUSD)
	if contextTokens > 0 {
		ev = fmt.Sprintf("context %s on %s; %s", formatTokens(contextTokens), model, ev)
	}
	return ev
}

// SessionPeaks is each session's highest API-equivalent spend in the
// ledger: what it had cost when last seen. dir "" is the default.
func SessionPeaks(dir string) map[string]float64 {
	f, err := os.Open(filepath.Join(resolveDir(dir), "events.jsonl")) //nolint:gosec // TokenOps' own ledger
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	peaks := map[string]float64{}
	dec := json.NewDecoder(f)
	for {
		var e ledgerEvent
		if err := dec.Decode(&e); err != nil {
			break
		}
		if e.CumulativeUSD > peaks[e.Session] {
			peaks[e.Session] = e.CumulativeUSD
		}
	}
	return peaks
}
