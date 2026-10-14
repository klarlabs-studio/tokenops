package budgets

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type spendSource struct{ cost float64 }

func (s *spendSource) Summarize(context.Context, analytics.Filter) (analytics.Summary, error) {
	return analytics.Summary{CostUSD: s.cost}, nil
}

func (s *spendSource) AggregateBy(context.Context, analytics.Filter, analytics.Bucket, analytics.Group) ([]analytics.Row, error) {
	return nil, nil
}

type events struct{ got []*eventschema.Envelope }

func (e *events) Publish(env *eventschema.Envelope) { e.got = append(e.got, env) }

var weekly = budget.Limit{Name: "weekly", Window: budget.WindowWeekly, LimitUSD: 100, Basis: budget.BasisSpend}

// An exceeded budget is recorded once a window, not once a tick: the
// audit log gained a budget_exceeded entry every watch interval.
func TestAnExceededBudgetIsPublishedOncePerWindow(t *testing.T) {
	src, pub := &spendSource{cost: 120}, &events{}
	w := &Watcher{Source: src, Limits: []budget.Limit{weekly}, Publish: pub}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	first := w.Tick(context.Background(), now)
	w.Tick(context.Background(), now.Add(15*time.Minute))
	if len(first.Alerts) != 1 || len(pub.got) != 1 {
		t.Errorf("alerts %d, events %d; want one of each", len(first.Alerts), len(pub.got))
	}
	w.Tick(context.Background(), now.AddDate(0, 0, 7))
	if len(pub.got) != 2 {
		t.Errorf("events %d; the next window's exceedance is a new one", len(pub.got))
	}
}

// A budget is exceeded when it reaches its limit, not when it warns.
func TestABudgetShortOfItsLimitPublishesNothing(t *testing.T) {
	src, pub := &spendSource{cost: 96}, &events{}
	w := &Watcher{Source: src, Limits: []budget.Limit{weekly}, Publish: pub}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if r := w.Tick(context.Background(), now); len(r.Alerts) != 1 || len(pub.got) != 0 {
		t.Fatalf("at 96%%: alerts %d, events %d; want a critical alert and no event", len(r.Alerts), len(pub.got))
	}
	src.cost = 101
	if r := w.Tick(context.Background(), now.Add(time.Hour)); len(r.Alerts) != 0 || len(pub.got) != 1 {
		t.Errorf("at 101%%: new alerts %d, events %d; want the same alert and one event", len(r.Alerts), len(pub.got))
	}
}

// The watcher forgets at restart; the audit log does not.
func TestAnExceedanceAlreadyRecordedIsNotRecordedAgain(t *testing.T) {
	pub := &events{}
	w := &Watcher{Source: &spendSource{cost: 150}, Limits: []budget.Limit{weekly}, Publish: pub,
		Recorded: func(_ context.Context, name string, _ time.Time) bool { return name == "weekly" }}
	w.Tick(context.Background(), time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	if len(pub.got) != 0 {
		t.Errorf("published %d events for a recorded exceedance", len(pub.got))
	}
}

// windowSource spends by window: the source is asked for one window at a
// time, and the window's start tells them apart.
type windowSource struct{ byDays map[int]float64 }

func (s *windowSource) Summarize(_ context.Context, f analytics.Filter) (analytics.Summary, error) {
	days := int(f.Until.Sub(f.Since).Hours() / 24)
	return analytics.Summary{CostUSD: s.byDays[days]}, nil
}

func (s *windowSource) AggregateBy(context.Context, analytics.Filter, analytics.Bucket, analytics.Group) ([]analytics.Row, error) {
	return nil, nil
}

// $10 a day and $100 a month, the day's window at 12h in and the month's
// at 6 days in.
var (
	teamDaily   = budget.Limit{Name: "team", ID: "team:daily", Window: budget.WindowDaily, LimitUSD: 10, Mode: budget.ModeHard}
	teamMonthly = budget.Limit{Name: "team", ID: "team:monthly", Window: budget.WindowMonthly, LimitUSD: 100}
	windowsNow  = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
)

func TestEachWindowIsMeasuredAndKeyedOnItsOwn(t *testing.T) {
	src := &windowSource{byDays: map[int]float64{0: 12, 6: 40}}
	limits := []budget.Limit{teamDaily, teamMonthly}
	got := UsedPct(context.Background(), src, limits, windowsNow)
	want := map[string]float64{"team:daily": 120, "team:monthly": 40, "team": 120}
	if len(got) != len(want) {
		t.Fatalf("used = %v; want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("used[%s] = %v; want %v", k, got[k], v)
		}
	}
	rs := Read(context.Background(), src, limits, windowsNow)
	if len(rs) != 2 {
		t.Fatalf("readings = %+v", rs)
	}
	d := rs[0]
	if d.Key != "team:daily" || d.Budget != "team" || d.Window != "daily" || d.Mode != "hard" ||
		d.Used != 12 || d.Limit != 10 || d.UsedPct != 120 || d.WarnPct != 75 || d.CritPct != 95 ||
		!d.ResetsAt.Equal(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("daily reading = %+v", d)
	}
}

// Two windows of one budget are two findings: the day's exceedance does
// not hide the month's, and each is recorded under its own key.
func TestWindowsOfOneBudgetAreRecordedApart(t *testing.T) {
	src := &windowSource{byDays: map[int]float64{0: 12, 6: 120}}
	pub := &events{}
	w := &Watcher{Source: src, Limits: []budget.Limit{teamDaily, teamMonthly}, Publish: pub}
	w.Tick(context.Background(), windowsNow)
	if len(pub.got) != 2 {
		t.Fatalf("published %d; want one per exceeded window", len(pub.got))
	}
	ids := map[string]bool{}
	for _, env := range pub.got {
		var e budget.Exceeded
		if err := json.Unmarshal(env.Payload.(*eventschema.DomainEvent).Data, &e); err != nil {
			t.Fatal(err)
		}
		ids[e.BudgetID] = true
	}
	if !ids["team:daily"] || !ids["team:monthly"] {
		t.Errorf("budget ids = %v; want both windows' keys", ids)
	}
}

func TestCurrentReadsOnlyAFreshMeasurement(t *testing.T) {
	dir := boundaryledger.Dir(t.TempDir())
	if got := Current(dir, windowsNow); got != nil {
		t.Fatalf("nothing measured, got %+v", got)
	}
	r := Reading{Key: "team:daily", Budget: "team", Window: "daily", UsedPct: 50}
	if err := dir.PutFigures(boundaryledger.Figures{ObservedAt: windowsNow, Budgets: []Reading{r}}); err != nil {
		t.Fatal(err)
	}
	if got := Current(dir, windowsNow.Add(5*time.Minute)); len(got) != 1 || got[0].Key != "team:daily" {
		t.Errorf("fresh reading = %+v", got)
	}
	if got := Current(dir, windowsNow.Add(time.Hour)); got != nil {
		t.Errorf("an hour-old reading was shown: %+v", got)
	}
}
