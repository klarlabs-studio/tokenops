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
