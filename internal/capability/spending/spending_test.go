package spending

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

type fakeAgg struct {
	rows []analytics.Row
	got  analytics.Filter
}

func (f *fakeAgg) AggregateBy(_ context.Context, flt analytics.Filter, _ analytics.Bucket, _ analytics.Group) ([]analytics.Row, error) {
	f.got = flt
	return f.rows, nil
}

// On a flat-rate plan every group costs $0, so the list is ranked on the
// API equivalent, then tokens, then the key.
func TestTopRanksOnTheAPIEquivalent(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	agg := &fakeAgg{rows: []analytics.Row{
		{GroupKey: "haiku", TotalTokens: 900, APIEquivalentUSD: 1},
		{GroupKey: "opus", TotalTokens: 100, APIEquivalentUSD: 40},
		{GroupKey: "opus", TotalTokens: 50, APIEquivalentUSD: 10},
		{GroupKey: "sonnet", TotalTokens: 900, APIEquivalentUSD: 1},
		{GroupKey: "metered", TotalTokens: 10, CostUSD: 0.5, APIEquivalentUSD: 0.5},
	}}
	got, err := Top(context.Background(), agg, TopQuery{Top: 3, IncludeSources: []string{" a ", "a", ""}}, "EUR", now)
	if err != nil {
		t.Fatal(err)
	}
	if got.By != "model" || got.Currency != "EUR" || len(got.Top) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got.Top[0].Key != "opus" || got.Top[0].APIEquivalentUSD != 50 || got.Top[1].Key != "haiku" || got.Top[2].Key != "sonnet" {
		t.Errorf("order %+v", got.Top)
	}
	if !agg.got.Since.Equal(now.Add(-7*24*time.Hour)) || len(agg.got.IncludeSources) != 1 {
		t.Errorf("filter %+v", agg.got)
	}
	if _, err := Top(context.Background(), agg, TopQuery{By: "colour"}, "EUR", now); !errors.Is(err, ErrUnknownGrouping) {
		t.Errorf("unknown grouping err = %v", err)
	}
}

func TestBurnRateTotalsTheWindow(t *testing.T) {
	agg := &fakeAgg{rows: []analytics.Row{{TotalTokens: 10, CostUSD: 1, APIEquivalentUSD: 2}, {TotalTokens: 5, APIEquivalentUSD: 3}}}
	got, err := BurnRate(context.Background(), agg, 0, nil, "USD", time.Now())
	if err != nil || got.Hours != 24 || got.Tokens != 15 || got.Cost != 1 || got.APIEquivalentUSD != 5 || len(got.Hourly) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestRateCardFilters(t *testing.T) {
	all := RateCard(t.TempDir(), RateQuery{Limit: 1000})
	if all.Models == 0 || len(all.Rates) == 0 {
		t.Fatalf("baseline card empty: %+v", all)
	}
	none := RateCard(t.TempDir(), RateQuery{Model: "no-such-model"})
	if len(none.Rates) != 0 || none.Note == "" {
		t.Errorf("filtered card %+v", none)
	}
}
