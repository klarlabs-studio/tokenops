package commits

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

type turnRecorder struct{ got analytics.Filter }

func (r *turnRecorder) SessionTurns(_ context.Context, f analytics.Filter) ([]analytics.SessionTurn, error) {
	r.got = f
	return nil, nil
}

// The report's since wins; the base's other bounds and sources are kept.
func TestTurnsInKeepsTheBaseFilter(t *testing.T) {
	until := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	since := until.AddDate(0, 0, -7)
	rec := &turnRecorder{}
	base := analytics.Filter{Since: until.AddDate(-1, 0, 0), Until: until, IncludeSources: []string{"mcp-session"}}
	if _, err := TurnsIn(rec, base)(context.Background(), since); err != nil {
		t.Fatal(err)
	}
	if !rec.got.Since.Equal(since) || !rec.got.Until.Equal(until) || len(rec.got.IncludeSources) != 1 {
		t.Errorf("filter %+v", rec.got)
	}
}
