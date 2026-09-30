package planswitch

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

type memHistory struct{ h plans.History }

func (m *memHistory) Load() (plans.History, error) { return m.h, nil }
func (m *memHistory) Append(bs ...plans.Binding) error {
	m.h = append(m.h, bs...)
	return nil
}

type fakeRestamper struct {
	provider string
	from, to time.Time
	calls    int
}

func (f *fakeRestamper) RestampPlanIncluded(_ context.Context, provider string, from, to time.Time) (int64, error) {
	f.provider, f.from, f.to = provider, from, to
	f.calls++
	return 305, nil
}

func day(d int) time.Time { return time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC) }

func TestBackdatedSwitchRecordsAndRestamps(t *testing.T) {
	h, r := &memHistory{}, &fakeRestamper{}
	now := day(30)
	res, err := Record(context.Background(), h, r, Change{Provider: "openai", Previous: "gpt-plus", Plan: "gpt-pro-5x", From: day(1), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if res.Restamped != 305 || r.provider != "openai" || !r.from.Equal(day(1)) || !r.to.Equal(now) {
		t.Fatalf("res=%+v restamper=%+v", res, r)
	}
	// The plan before the first recorded switch is kept.
	if len(h.h) != 2 || h.h[0].Plan != "gpt-plus" || h.h[1].Plan != "gpt-pro-5x" {
		t.Fatalf("history = %+v", h.h)
	}
}

func TestSwitchNowAndUnsetDoNotRestamp(t *testing.T) {
	h, r := &memHistory{}, &fakeRestamper{}
	if _, err := Record(context.Background(), h, r, Change{Provider: "openai", Plan: "gpt-plus", From: day(30), Now: day(30)}); err != nil {
		t.Fatal(err)
	}
	if _, err := Record(context.Background(), h, r, Change{Provider: "openai", Previous: "gpt-plus", From: day(10), Now: day(30)}); err != nil {
		t.Fatal(err)
	}
	if r.calls != 0 {
		t.Fatalf("restamped %d times", r.calls)
	}
}

func TestFutureStartIsRefused(t *testing.T) {
	_, err := Record(context.Background(), &memHistory{}, nil, Change{Provider: "openai", Plan: "gpt-plus", From: day(30), Now: day(1)})
	if !errors.Is(err, ErrFuture) {
		t.Fatalf("err = %v", err)
	}
}

// A mid-month switch prices each part at its own plan.
func TestCostProratesAcrossASwitch(t *testing.T) {
	h := plans.History(plans.History(nil).Switch("openai", "gpt-plus", "gpt-pro-5x", day(16), day(16)))
	costs := Cost(h, map[string]string{"openai": "gpt-pro-5x", "anthropic": "claude-max-20x"}, day(1), day(31), FX{})
	if len(costs) != 2 {
		t.Fatalf("costs = %+v", costs)
	}
	anthropic, openai := costs[0], costs[1]
	perDay := func(m float64) float64 { return m / (365.2425 / 12) }
	if math.Abs(anthropic.Amount-30*perDay(200)) > 0.01 || !anthropic.Complete {
		t.Errorf("anthropic = %+v", anthropic)
	}
	want := 15*perDay(20) + 15*perDay(100)
	if len(openai.Periods) != 2 || math.Abs(openai.Amount-want) > 0.01 {
		t.Errorf("openai = %+v, want %.2f", openai, want)
	}
	if usd, complete := Total(costs); !complete || math.Abs(usd-anthropic.Amount-openai.Amount) > 1e-9 {
		t.Errorf("Total = %.2f %v", usd, complete)
	}
}

func TestCostFlagsUnpricedPlans(t *testing.T) {
	costs := Cost(nil, map[string]string{"anthropic": "claude-enterprise"}, day(1), day(31), FX{})
	if len(costs) != 1 || costs[0].Complete || costs[0].Amount != 0 {
		t.Fatalf("costs = %+v", costs)
	}
}

// The operator's own price, in their currency, wins over the list price;
// a list-priced period converts at their rate; a currency with no rate
// is flagged rather than guessed.
func TestCostUsesTheOperatorsPriceAndCurrency(t *testing.T) {
	h := &memHistory{}
	if _, err := Record(context.Background(), h, nil, Change{
		Provider: "anthropic", Plan: "claude-max-20x", From: day(1), Now: day(1), Price: 214.60, Currency: "eur",
	}); err != nil {
		t.Fatal(err)
	}
	perDay := func(m float64) float64 { return m / (365.2425 / 12) }
	fx := FX{Currency: "EUR", PerUSD: 0.85}
	costs := Cost(h.h, map[string]string{"anthropic": "claude-max-20x", "openai": "gpt-plus"}, day(1), day(11), fx)
	anthropic, openai := costs[0], costs[1]
	if p := anthropic.Periods[0]; p.Source != "yours" || math.Abs(p.Amount-10*perDay(214.60)) > 0.01 {
		t.Errorf("anthropic = %+v", p)
	}
	if p := openai.Periods[0]; p.Source != "list" || math.Abs(p.Amount-10*perDay(20)*0.85) > 0.01 {
		t.Errorf("openai = %+v", p)
	}
	// Shown in USD with no rate, the EUR price cannot be converted.
	usd := Cost(h.h, map[string]string{"anthropic": "claude-max-20x"}, day(1), day(11), FX{})
	if usd[0].Complete || usd[0].Periods[0].Priced {
		t.Errorf("converted EUR without a rate: %+v", usd[0])
	}
}
