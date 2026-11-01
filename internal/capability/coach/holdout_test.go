package coach

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

// A held-back move reaches the verdict as a held run of the move it was
// held from, and is never counted as advice the operator ignored.
func TestHeldMovesAreEvidenceNotInterventions(t *testing.T) {
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	l := &memLedger{}
	RecordSubagentHoldout(l, at, "h1", "s1", "toolu_1", "edit", "claude-opus-5", "claude-sonnet-5", "held back at random, 10% of moves")
	RecordSubagentMove(l, at, "m1", "s1", "toolu_2", "edit", "claude-opus-5", "claude-sonnet-5", "")
	moves := subagentMoves(l)
	if h := moves["toolu_1"]; !h.Held || h.From != "opus" || h.To != "sonnet" {
		t.Errorf("holdout = %+v", h)
	}
	if m := moves["toolu_2"]; m.Held {
		t.Errorf("a move read as held: %+v", m)
	}
	if n := CountMoves(l, config.PowerModels); n != 1 {
		t.Errorf("moves counted = %d, want the one made", n)
	}
	if kept := ft.Prune(l.entries, at.Add(time.Hour), 28*24*time.Hour); len(kept) != 2 {
		t.Errorf("pruning dropped the holdout: %+v", kept)
	}
}
