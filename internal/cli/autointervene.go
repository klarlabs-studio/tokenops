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

// promotionCase argues for letting the guard start refusing redundant
// re-reads, using the operator's own ledger and naming the one command
// that acts on it. Empty when there is no case to make.
//
// It never promotes anything. #240 removed exactly that behaviour from
// `init`: flipping a hook from observing to refusing the agent's reads is
// what `intervene` exists to gate, and a tool that crosses that line on
// its own has decided the question the gate was there to ask.
//
// A guard that has already blocked something is not argued with. The
// operator either promoted it deliberately or pinned `--mode=active` on
// the hook; either way the case has been made and won, and repeating it
// is nagging about a decision already taken.
func promotionCase(s readguard.Stats) string {
	if s.Blocked > 0 || s.ReclaimedTok > 0 {
		return ""
	}
	if s.ReclaimableTok < minReclaimableTokens {
		return ""
	}
	return fmt.Sprintf(
		"tokenops: read-guard has watched ~%s tokens of redundant re-reads go by across %d sessions "+
			"— %d reads it would have refused, and did not. "+
			"`tokenops coach delivery intervene` lets it refuse them before they cost anything.",
		humanTokens(s.ReclaimableTok), s.DistinctSessions, s.WouldBlock)
}
