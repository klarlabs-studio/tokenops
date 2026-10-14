package anomalies

import (
	"context"
	"math"
	"testing"
	"time"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/anomaly"
)

var now = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

func midnightAgo(n int) time.Time {
	return time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -n)
}

// memLedger is an in-memory follow-through ledger.
type memLedger struct{ entries []ft.Entry }

func (m *memLedger) Append(e ...ft.Entry) error { m.entries = append(m.entries, e...); return nil }
func (m *memLedger) Load() ([]ft.Entry, error)  { return m.entries, nil }

// Turns become items with their repository, tool and route; what the
// daily charges hold beyond the session turns stays in, outside a
// session, so the items sum to the day's spend and usage.
func TestItemsJoinTurnsAndKeepTheRestOutsideASession(t *testing.T) {
	day := midnightAgo(1)
	turns := []analytics.SessionTurn{
		{SessionID: "s1", At: day.Add(time.Hour), Provider: "anthropic", Model: "sonnet", Tokens: 100, APIEquivalentUSD: 3, PlanCovered: true, Source: "claude-code-jsonl"},
		{SessionID: "s1", At: day.Add(2 * time.Hour), Provider: "anthropic", Model: "sonnet", Tokens: 100, APIEquivalentUSD: 3, PlanCovered: true, Source: "claude-code-jsonl"},
	}
	charges := []analytics.Charge{
		{Day: day, Provider: "anthropic", Model: "sonnet", PlanCovered: true, InputTokens: 250, ListUSD: 8},
		{Day: day, Provider: "openai", Model: "gpt", InputTokens: 50, CostUSD: 1, ListUSD: 1},
	}
	repoOf := map[string]string{"s1": "tokenops"}
	items := Items(turns, charges, repoOf)
	var value, cost float64
	var bySession = map[string]float64{}
	for _, it := range items {
		value += it.ValueUSD
		cost += it.CostUSD
		bySession[it.Session] += it.ValueUSD
		if it.Session == "s1" && (it.Repo != "tokenops" || it.Tool != "claude-code-jsonl") {
			t.Errorf("turn item = %+v", it)
		}
	}
	if math.Abs(value-9) > 1e-9 || math.Abs(cost-1) > 1e-9 {
		t.Errorf("items sum to value %v cost %v; want 9 and 1", value, cost)
	}
	if math.Abs(bySession["s1"]-6) > 1e-9 || math.Abs(bySession[""]-3) > 1e-9 {
		t.Errorf("by session = %v", bySession)
	}
}

func anomalyOn(id, pattern string, day time.Time) anomaly.Anomaly {
	return anomaly.Anomaly{ID: id, Metric: anomaly.MetricUsage, Day: day, Cause: anomaly.Cause{Kind: anomaly.CauseSpike, Pattern: pattern}}
}

// An anomaly is offered once, as an ambient tip the operator can answer.
func TestRecordOffersEachAnomalyOnce(t *testing.T) {
	l := &memLedger{}
	s := Snapshot{Anomalies: []Anomaly{{Anomaly: anomalyOn("anomaly-usage-2026-10-14", "spike:usage", midnightAgo(1))}}}
	Record(l, s, now)
	Record(l, s, now.Add(time.Hour))
	if len(l.entries) != 1 {
		t.Fatalf("entries = %+v", l.entries)
	}
	e := l.entries[0]
	if e.Type != ft.EntryOffer || e.Channel != ft.ChannelTip || e.Kind != "anomaly_spike" || e.Via != ft.ViaAmbient || e.Evidence != "spike:usage" {
		t.Errorf("offer = %+v", e)
	}
}

// Acknowledged, snoozed and expected anomalies stop showing; "expected"
// also stops the same pattern on later days.
func TestOpenLeavesOutWhatWasAnswered(t *testing.T) {
	old := anomalyOn("anomaly-usage-2026-10-10", "pricier_model:opus", midnightAgo(5))
	acked := anomalyOn("anomaly-usage-2026-10-14", "spike:usage", midnightAgo(1))
	same := anomalyOn("anomaly-spend-2026-10-15", "pricier_model:opus", midnightAgo(0))
	other := anomalyOn("anomaly-usage-2026-10-15", "runaway_loop:warden", midnightAgo(0))
	l := &memLedger{}
	Record(l, Snapshot{Anomalies: []Anomaly{{Anomaly: old}, {Anomaly: acked}}}, now.Add(-5*24*time.Hour))
	_ = l.Append(
		ft.Entry{Type: ft.EntryFeedback, ID: old.ID, At: now.Add(-4 * 24 * time.Hour), Outcome: ft.OutcomeExpected},
		ft.Entry{Type: ft.EntryFeedback, ID: acked.ID, At: now.Add(-time.Hour), Outcome: ft.OutcomeAcknowledged},
	)
	s := Snapshot{ComputedAt: now, Anomalies: []Anomaly{{Anomaly: acked}, {Anomaly: same}, {Anomaly: other}}}
	got := Open(s, l, now)
	if len(got) != 1 || got[0].ID != other.ID {
		t.Fatalf("open = %+v; want only the loop", got)
	}

	// A snooze on any anomaly holds them all until it ends.
	Record(l, s, now)
	_ = l.Append(ft.Entry{Type: ft.EntryFeedback, ID: other.ID, At: now, Outcome: ft.OutcomeSnoozed, Until: now.Add(4 * time.Hour)})
	if got := Open(s, l, now.Add(time.Hour)); len(got) != 0 {
		t.Errorf("snoozed anomalies still open: %+v", got)
	}
}

// A stale reading of days long past says nothing about now.
func TestOpenDropsDaysBeforeYesterday(t *testing.T) {
	s := Snapshot{ComputedAt: now, Anomalies: []Anomaly{{Anomaly: anomalyOn("a", "p", midnightAgo(3))}}}
	if got := Open(s, nil, now); len(got) != 0 {
		t.Errorf("open = %+v", got)
	}
}

// Run detects, names the waste the detector saw in a runaway session,
// records the offer and stores the snapshot.
func TestRunStoresTheSnapshotAndOffersIt(t *testing.T) {
	var turns []analytics.SessionTurn
	var charges []analytics.Charge
	for n := 1; n <= 21; n++ {
		d := midnightAgo(n)
		usd := 10.0
		session := "s" + d.Format("0102")
		wf := ""
		if n == 1 {
			usd, session, wf = 70, "loop", "claude-code:p:loop"
		}
		turns = append(turns, analytics.SessionTurn{SessionID: session, At: d.Add(time.Hour), Provider: "anthropic", Model: "sonnet",
			Tokens: int64(usd * 1e5), APIEquivalentUSD: usd, PlanCovered: true, Source: "claude-code-jsonl", WorkflowID: wf})
		charges = append(charges, analytics.Charge{Day: d, Provider: "anthropic", Model: "sonnet", PlanCovered: true, InputTokens: int64(usd * 1e5), ListUSD: usd})
	}
	dir := t.TempDir()
	l := &memLedger{}
	d := Deps{
		Turns:   func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return turns, nil },
		Charges: func(context.Context, time.Time, time.Time) ([]analytics.Charge, error) { return charges, nil },
		RepoOf:  func(context.Context, time.Time) map[string]string { return map[string]string{"loop": "warden"} },
		Waste: func(_ context.Context, wf string) []string {
			if wf == "claude-code:p:loop" {
				return []string{"Repeated agent loop detected"}
			}
			return nil
		},
		Ledger: l,
	}
	s, err := Run(context.Background(), d, dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Anomalies) != 1 || s.Anomalies[0].Cause.Kind != anomaly.CauseRunawayLoop {
		t.Fatalf("anomalies = %+v", s.Anomalies)
	}
	a := s.Anomalies[0]
	if a.Workflow != "claude-code:p:loop" || len(a.Waste) != 1 {
		t.Errorf("waste link = %q %v", a.Workflow, a.Waste)
	}
	if len(l.entries) != 1 || l.entries[0].ID != a.ID {
		t.Errorf("ledger = %+v", l.entries)
	}
	back, err := ReadSnapshot(dir)
	if err != nil || back == nil || len(back.Anomalies) != 1 || back.HistoryDays < 20 {
		t.Fatalf("snapshot = %+v, %v", back, err)
	}
}
