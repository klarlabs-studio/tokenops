package avoidable

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// The stopped work is the turn in flight when the developer stopped the
// instruction: sent before the stop, recorded after it. Only that turn is
// claimed, and only when its send time is known, so waste is never
// inflated by turns that finished before the stop.
func TestStoppedWorkIsTheTurnInFlightAtTheStop(t *testing.T) {
	inFlight := func(at time.Duration, latency time.Duration) analytics.SessionTurn {
		tn := turn("s", at)
		tn.Latency = latency
		return tn
	}
	stop := Stop{Session: "s", From: t0, At: t0.Add(90 * time.Second)}
	cases := []struct {
		name       string
		turns      []analytics.SessionTurn
		stop       Stop
		wantTokens int64
		wantTurns  int
	}{
		{
			name:       "the turn sent before the stop and recorded after it",
			turns:      []analytics.SessionTurn{inFlight(0, time.Minute), inFlight(100*time.Second, 10*time.Second)},
			stop:       stop,
			wantTokens: 1_000, wantTurns: 1,
		},
		{
			name:       "a turn recorded before the stop finished before it",
			turns:      []analytics.SessionTurn{inFlight(time.Minute, 10*time.Second), inFlight(3*time.Minute, 10*time.Second)},
			stop:       stop,
			wantTokens: 0, wantTurns: 0,
		},
		{
			name:       "a turn sent after the stop was not in flight",
			turns:      []analytics.SessionTurn{inFlight(100*time.Second, 10*time.Second)},
			stop:       Stop{Session: "s", From: t0, At: t0.Add(time.Minute)},
			wantTokens: 0, wantTurns: 0,
		},
		{
			name:       "without a recorded latency the send time is unknown, so nothing is claimed",
			turns:      []analytics.SessionTurn{inFlight(100*time.Second, 0)},
			stop:       stop,
			wantTokens: 0, wantTurns: 0,
		},
		{
			name: "turns of another session are not stopped",
			turns: []analytics.SessionTurn{func() analytics.SessionTurn {
				tn := turn("other", 100*time.Second)
				tn.Latency = 10 * time.Second
				return tn
			}()},
			stop:       stop,
			wantTokens: 0, wantTurns: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := Inputs{Since: t0, Until: t0.Add(day), Turns: tc.turns, Stops: []Stop{tc.stop}, Price: price}
			r := Build(in)
			c := classOf(t, r, waste.ClassStopped)
			if c.Tokens != tc.wantTokens || c.Turns != tc.wantTurns {
				t.Errorf("stopped = %d tokens over %d turns; want %d over %d", c.Tokens, c.Turns, tc.wantTokens, tc.wantTurns)
			}
		})
	}
}

func TestConcentrationNamesTheSessionsThatMakeHalf(t *testing.T) {
	cases := []struct {
		name     string
		usd      map[string]float64
		top      int
		topShare float64
	}{
		{"one session makes most of it", map[string]float64{"a": 6, "b": 3, "c": 1}, 1, 60},
		{"two sessions make half", map[string]float64{"a": 3, "b": 2, "c": 2, "d": 1}, 2, 62.5},
		{"a spread of equals needs half of them", map[string]float64{"a": 1, "b": 1, "c": 1, "d": 1}, 2, 50},
		{"a single session", map[string]float64{"a": 4}, 1, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ts []TurnWaste
			for s, usd := range tc.usd {
				ts = append(ts, TurnWaste{Turn: analytics.SessionTurn{SessionID: s}, Class: waste.ClassStopped, APIEquivalentUSD: usd, Tokens: 1})
			}
			got := concentrationOf(ts, waste.ClassStopped)
			if got == nil {
				t.Fatal("no concentration")
			}
			if got.Top != tc.top || got.TopPct != tc.topShare || got.Sessions != len(tc.usd) {
				t.Errorf("got %+v; want top %d at %v%% of %d sessions", *got, tc.top, tc.topShare, len(tc.usd))
			}
		})
	}
}

func classOf(t *testing.T, r Report, c waste.Class) ClassCost {
	t.Helper()
	for _, cc := range r.Classes {
		if cc.Class == c {
			return cc
		}
	}
	t.Fatalf("no class %s in the report", c)
	return ClassCost{}
}
