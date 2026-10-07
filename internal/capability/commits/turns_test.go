package commits

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

type fakeTurns struct{ got analytics.Filter }

func (f *fakeTurns) SessionTurns(_ context.Context, flt analytics.Filter) ([]analytics.SessionTurn, error) {
	f.got = flt
	return []analytics.SessionTurn{{SessionID: "s"}}, nil
}

func TestTurnsFromReadsSince(t *testing.T) {
	r := &fakeTurns{}
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	turns, err := TurnsFrom(r)(context.Background(), since)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || !r.got.Since.Equal(since) || !r.got.Until.IsZero() {
		t.Errorf("turns = %v, filter = %+v", turns, r.got)
	}
}
