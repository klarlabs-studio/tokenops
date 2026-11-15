package daemon

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// A daemon stopping before the first analysis returns at once instead of
// waiting the analysis out.
func TestSessionFindingsStopsBeforeTheFirstRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		runSessionFindings(ctx, t.TempDir(), nil, nil, time.Hour, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not stop")
	}
}

// A unit's cost is the list-price cost of the store's turns for its
// session that fell inside it; a session the store has no turns for
// costs nothing.
func TestCostOfTurnsPricesEachUnitFromItsSession(t *testing.T) {
	base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	turns := []analytics.SessionTurn{
		{SessionID: "s", At: base.Add(time.Minute), APIEquivalentUSD: 1.5},
		{SessionID: "s", At: base.Add(time.Hour), APIEquivalentUSD: 9},
		{SessionID: "other", At: base.Add(time.Minute), APIEquivalentUSD: 7},
	}
	cost := costOfTurns(turns)
	if got := cost(agentdx.Unit{SessionID: "s", Start: base, LastActivity: base.Add(5 * time.Minute)}); got != 1.5 {
		t.Errorf("unit cost = %v, want 1.5", got)
	}
	if got := cost(agentdx.Unit{SessionID: "none", Start: base, LastActivity: base.Add(time.Hour)}); got != 0 {
		t.Errorf("unknown session costs %v", got)
	}
}
