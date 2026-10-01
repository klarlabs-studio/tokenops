package plans

import (
	"context"
	"testing"
	"time"
)

// A spend-denominated plan read from Claude Code's own logs was graded
// the lowest signal quality, because only window plans were graded.
func TestSpendPlanSignalIsGraded(t *testing.T) {
	now := time.Date(2026, 10, 1, 18, 0, 0, 0, time.UTC)
	var since time.Time
	counts := func(_ context.Context, from, _ time.Time) (map[string]int64, error) {
		since = from
		return map[string]int64{"claude-code-jsonl": 5136}, nil
	}
	in, err := AssembleHeadroomInputs(context.Background(), fakeReader{}, counts, "anthropic", "claude-enterprise",
		SpendLimit{LimitUSD: 500, Window: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := ClassifySignal(in.Signal); got.Level != SignalLevelHigh {
		t.Errorf("signal %+v, want high from Claude Code logs", got)
	}
	if want := SpendWindowStart(now, "monthly"); !since.Equal(want) {
		t.Errorf("graded from %v, want the spend window start %v", since, want)
	}
}
