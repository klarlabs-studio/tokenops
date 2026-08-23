package cli

import (
	"fmt"

	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

// minReclaimableTokens is the evidence bar for switching the read guard
// from logging waste to preventing it.
//
// Roughly a large file's worth of context. Below it the intervention is
// not worth the risk of blocking a read the agent genuinely wanted; above
// it, staying in observe mode means knowingly watching tokens burn.
const minReclaimableTokens = 50_000

// guardModeFor picks the read-guard mode from its own ledger, and returns
// the reason so the operator is told why rather than having a behaviour
// change appear unannounced.
//
// The guard shipped observe-first on purpose: a PreToolUse hard block is a
// real intervention in the agent's flow, and turning it on before anyone
// had seen reclaimable numbers would have been optimising on faith. This
// promotes it once the operator's own ledger has made the case.
func guardModeFor(s readguard.Stats) (readguard.Mode, string) {
	// Once active, the reclaimed figure is the evidence. would-block falls
	// to zero precisely because the guard is working, so judging on that
	// alone would flip it back off the moment it succeeded.
	if s.ReclaimedTok >= minReclaimableTokens {
		return readguard.ModeActive, fmt.Sprintf(
			"active — already reclaimed ~%s tokens across %d sessions",
			humanTokens(s.ReclaimedTok), s.DistinctSessions)
	}
	if s.ReclaimableTok >= minReclaimableTokens {
		return readguard.ModeActive, fmt.Sprintf(
			"active — your ledger shows ~%s tokens of redundant re-reads across %d sessions",
			humanTokens(s.ReclaimableTok), s.DistinctSessions)
	}
	if s.Events == 0 {
		return readguard.ModeObserve, "observing — no read history yet; it activates once redundant re-reads are measured"
	}
	return readguard.ModeObserve, fmt.Sprintf(
		"observing — only ~%s tokens reclaimable so far, below the %s threshold to start blocking",
		humanTokens(s.ReclaimableTok), humanTokens(minReclaimableTokens))
}
