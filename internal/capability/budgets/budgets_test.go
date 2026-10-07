package budgets

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
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
