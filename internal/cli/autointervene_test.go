package cli

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

// The guard shipped in observe mode deliberately: blocking re-reads before
// anyone had seen real numbers risked degrading agent flow for nothing.
// Once the ledger shows reclaimable tokens, staying in observe means
// knowingly logging waste instead of preventing it.
func TestGuardModeActivatesOnMeasuredWaste(t *testing.T) {
	mode, why := guardModeFor(readguard.Stats{
		Events: 5068, WouldBlock: 225, ReclaimableTok: 397054, DistinctSessions: 22,
	})
	if mode != readguard.ModeActive {
		t.Errorf("mode = %q, want active with 397k reclaimable", mode)
	}
	if !strings.Contains(why, "397") {
		t.Errorf("reason should cite the measured number, got %q", why)
	}
}

// With no ledger yet there is nothing to justify blocking, so it observes
// first and says so. Turning on an intervention before measuring is how a
// tool degrades a workflow for a saving it never demonstrated.
func TestGuardModeObservesWithoutEvidence(t *testing.T) {
	mode, why := guardModeFor(readguard.Stats{})
	if mode != readguard.ModeObserve {
		t.Errorf("mode = %q, want observe with an empty ledger", mode)
	}
	if !strings.Contains(strings.ToLower(why), "observ") {
		t.Errorf("reason should explain the observe choice, got %q", why)
	}
}

// A trickle of waste is not worth blocking over — the threshold keeps the
// intervention proportionate to what it recovers.
func TestGuardModeObservesBelowThreshold(t *testing.T) {
	mode, _ := guardModeFor(readguard.Stats{
		Events: 40, WouldBlock: 1, ReclaimableTok: 900, DistinctSessions: 2,
	})
	if mode != readguard.ModeObserve {
		t.Errorf("mode = %q, want observe for 900 reclaimable tokens", mode)
	}
}

// Already-active ledgers keep blocking: the reclaimed figure is the
// evidence once the guard is doing its job, and it must not flip back to
// observe just because would-block dropped to zero as a result.
func TestGuardModeStaysActiveOnReclaimedEvidence(t *testing.T) {
	mode, _ := guardModeFor(readguard.Stats{
		Events: 5000, Blocked: 215, ReclaimedTok: 387152, DistinctSessions: 22,
	})
	if mode != readguard.ModeActive {
		t.Errorf("mode = %q, want active — the guard is already recovering tokens", mode)
	}
}

// The case is made with the operator's own numbers and names the one
// command that acts on them. A recommendation without the command is a
// complaint.
func TestPromotionCaseCitesTheLedgerAndTheCommand(t *testing.T) {
	msg := promotionCase(readguard.Stats{
		Events: 5068, WouldBlock: 225, ReclaimableTok: 397054, DistinctSessions: 22,
	})
	if msg == "" {
		t.Fatal("no case made for 397k reclaimable tokens across 22 sessions")
	}
	for _, want := range []string{"397", "22", "225", "tokenops coach delivery intervene"} {
		if !strings.Contains(msg, want) {
			t.Errorf("case %q is missing %q", msg, want)
		}
	}
}

// Below the evidence bar there is nothing to argue. Asking for an
// intervention the numbers do not justify is how a tool teaches operators
// to ignore it.
func TestPromotionCaseSilentWithoutEvidence(t *testing.T) {
	for _, s := range []readguard.Stats{
		{},
		{Events: 40, WouldBlock: 1, ReclaimableTok: 900, DistinctSessions: 2},
	} {
		if msg := promotionCase(s); msg != "" {
			t.Errorf("promotionCase(%+v) = %q, want no case", s, msg)
		}
	}
}

// A guard that has already blocked something has had this argument and
// won it — whether by `coach delivery intervene` or a pinned
// `--mode=active`. Repeating the case is nagging about a settled
// decision.
func TestPromotionCaseSilentOnceTheGuardBlocks(t *testing.T) {
	s := readguard.Stats{
		Events: 5000, Blocked: 215, ReclaimedTok: 387152,
		ReclaimableTok: 400_000, DistinctSessions: 22,
	}
	if msg := promotionCase(s); msg != "" {
		t.Errorf("promotionCase on an already-blocking guard = %q, want silence", msg)
	}
}
