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

// A zero base reads every turn since since.
func TestTurnsInReadsSince(t *testing.T) {
	r := &fakeTurns{}
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	turns, err := TurnsIn(r, analytics.Filter{})(context.Background(), since)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || !r.got.Since.Equal(since) || !r.got.Until.IsZero() {
		t.Errorf("turns = %v, filter = %+v", turns, r.got)
	}
}

// The report's since wins; the base's other bounds and sources are kept.
func TestTurnsInKeepsTheBaseFilter(t *testing.T) {
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	since := until.AddDate(0, 0, -7)
	r := &fakeTurns{}
	base := analytics.Filter{Since: until.AddDate(-1, 0, 0), Until: until, IncludeSources: []string{"mcp-session"}}
	if _, err := TurnsIn(r, base)(context.Background(), since); err != nil {
		t.Fatal(err)
	}
	if !r.got.Since.Equal(since) || !r.got.Until.Equal(until) || len(r.got.IncludeSources) != 1 {
		t.Errorf("filter %+v", r.got)
	}
}
