package spending

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

func TestRollUpFoldsBucketsAndRanks(t *testing.T) {
	day1 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rows := []Row{
		{BucketStart: day1, GroupKey: "b", Requests: 1, InputTokens: 9, TotalTokens: 10, APIEquivalentUSD: 1},
		{BucketStart: day1, GroupKey: "a", Requests: 1, InputTokens: 9, TotalTokens: 10, APIEquivalentUSD: 1},
		{BucketStart: day1, GroupKey: "opus", Requests: 1, InputTokens: 90, OutputTokens: 10, TotalTokens: 100, CostUSD: 1, APIEquivalentUSD: 4},
		{BucketStart: day1.AddDate(0, 0, 1), GroupKey: "opus", Requests: 2, InputTokens: 9, OutputTokens: 1, TotalTokens: 10, CostUSD: 2, APIEquivalentUSD: 1},
	}
	got := RollUp(rows, 0)
	if len(got) != 3 || got[0].GroupKey != "opus" || got[1].GroupKey != "a" || got[2].GroupKey != "b" {
		t.Fatalf("order %+v", got)
	}
	opus := got[0]
	if opus.Requests != 3 || opus.InputTokens != 99 || opus.OutputTokens != 11 || opus.TotalTokens != 110 ||
		opus.CostUSD != 3 || opus.APIEquivalentUSD != 5 || !opus.BucketStart.Equal(day1) {
		t.Errorf("opus %+v", opus)
	}
	if len(RollUp(rows, 2)) != 2 || RollUp(nil, 3) != nil {
		t.Error("limit or empty input")
	}
}

func TestGroupOf(t *testing.T) {
	for by, want := range map[string]Group{"": analytics.GroupModel, "model": analytics.GroupModel, "agent": analytics.GroupAgent} {
		if got, ok := GroupOf(by); !ok || got != want {
			t.Errorf("GroupOf(%q) = %q, %v", by, got, ok)
		}
	}
	if _, ok := GroupOf("Model"); ok {
		t.Error("GroupOf normalises; callers do")
	}
}

// fakeSource answers Summarize and records each AggregateBy call.
type fakeSource struct {
	totals Totals
	rows   map[analytics.Bucket][]Row
	calls  []Filter
}

func (f *fakeSource) Summarize(context.Context, Filter) (Totals, error) { return f.totals, nil }

func (f *fakeSource) AggregateBy(_ context.Context, flt Filter, b analytics.Bucket, _ analytics.Group) ([]Row, error) {
	f.calls = append(f.calls, flt)
	return f.rows[b], nil
}

func TestSpendReport(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{
		totals: Totals{Requests: 4},
		rows: map[analytics.Bucket][]Row{
			analytics.BucketDay: {
				{BucketStart: now.AddDate(0, 0, -2), GroupKey: "m", CostUSD: 1, TotalTokens: 10},
				{BucketStart: now.AddDate(0, 0, -1), GroupKey: "m", CostUSD: 2, TotalTokens: 20},
			},
			analytics.BucketHour: {{CostUSD: 0.5, TotalTokens: 5}, {CostUSD: 0.25, TotalTokens: 7}},
		},
	}
	q := ReportQuery{Filter: Filter{IncludeSources: []string{"mcp-session"}}, Group: analytics.GroupModel, Top: 5}
	got, err := SpendReport(context.Background(), src, q, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Totals.Requests != 4 || len(got.Top) != 1 || got.Top[0].CostUSD != 3 || got.BurnCost != 0.75 || got.BurnTokens != 12 {
		t.Errorf("report %+v", got)
	}
	if got.Forecast != nil || got.ForecastTokens != nil {
		t.Error("forecast without asking")
	}
	burn := src.calls[1]
	if !burn.Since.Equal(now.Add(-24*time.Hour)) || burn.IncludeSources != nil {
		t.Errorf("burn filter %+v", burn)
	}

	q.Forecast = true
	got, err = SpendReport(context.Background(), src, q, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Forecast) != 7 || len(got.ForecastTokens) != 7 {
		t.Errorf("forecast %d/%d points, want the default seven", len(got.Forecast), len(got.ForecastTokens))
	}
}

func TestWindow(t *testing.T) {
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := map[string]Filter{
		"all time":                   {},
		"since=2026-01-01T00:00:00Z": {Since: since},
		"since=2026-01-01T00:00:00Z until=2026-01-02T00:00:00Z": {Since: since, Until: since.AddDate(0, 0, 1)},
	}
	for want, f := range cases {
		if got := Window(f); got != want {
			t.Errorf("Window = %q, want %q", got, want)
		}
	}
}
