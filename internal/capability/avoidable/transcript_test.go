package avoidable

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
)

// The stops are the instructions the transcripts record as interrupted,
// each with the turns the store holds for its session.
func TestComputeTakesStopsFromTheTranscripts(t *testing.T) {
	// The turn sent at 1m40s and recorded at 2m10s was in flight when the
	// developer stopped the instruction at 2m.
	inFlight := turn("s", 130*time.Second)
	inFlight.Latency = 30 * time.Second
	turns := []analytics.SessionTurn{turn("s", time.Minute), inFlight}
	records := []agentdx.Record{
		{At: t0, SessionID: "s", Kind: agentdx.KindPrompt},
		{At: t0.Add(2 * time.Minute), SessionID: "s", Kind: agentdx.KindInterrupt},
	}
	d := Deps{
		Turns: func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return turns, nil },
		Dirs:  func(time.Time) map[string]sessiondirs.Dir { return map[string]sessiondirs.Dir{} },
		Price: price,
		Records: func(context.Context, time.Time, time.Time) ([]agentdx.Record, error) {
			return records, nil
		},
	}
	r, err := Compute(context.Background(), d, t0, t0.Add(day))
	if err != nil {
		t.Fatal(err)
	}
	if got := classOf(t, r, waste.ClassStopped); got.Tokens != 1_000 || got.Turns != 1 {
		t.Errorf("stopped = %+v; want the one turn before the stop", got)
	}
}

// A transcript read that fails leaves stops and the context line out; it
// does not fail the report.
func TestComputeSurvivesAnUnreadableTranscript(t *testing.T) {
	d := Deps{
		Turns: func(context.Context, time.Time) ([]analytics.SessionTurn, error) {
			return []analytics.SessionTurn{turn("s", time.Minute)}, nil
		},
		Dirs:  func(time.Time) map[string]sessiondirs.Dir { return map[string]sessiondirs.Dir{} },
		Price: price,
		Records: func(context.Context, time.Time, time.Time) ([]agentdx.Record, error) {
			return nil, context.DeadlineExceeded
		},
	}
	r, err := Compute(context.Background(), d, t0, t0.Add(day))
	if err != nil {
		t.Fatalf("report failed on an unreadable transcript: %v", err)
	}
	if r.ContextLine != nil {
		t.Errorf("context line from no transcript: %+v", r.ContextLine)
	}
	if got := classOf(t, r, waste.ClassStopped); got.Tokens != 0 {
		t.Errorf("stopped work from no transcript: %+v", got)
	}
}
