package spending

import (
	"context"
	"reflect"
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
	calls  []Window
}

func (f *fakeSource) Summarize(context.Context, Window) (Totals, error) { return f.totals, nil }

func (f *fakeSource) AggregateBy(_ context.Context, flt Window, b analytics.Bucket, _ analytics.Group) ([]Row, error) {
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
	q := ReportQuery{Filter: Window{IncludeSources: []string{"mcp-session"}}, Group: analytics.GroupModel, Top: 5}
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

func TestWindowLabel(t *testing.T) {
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := map[string]Window{
		"all time":                   {},
		"since=2026-01-01T00:00:00Z": {Since: since},
		"since=2026-01-01T00:00:00Z until=2026-01-02T00:00:00Z": {Since: since, Until: since.AddDate(0, 0, 1)},
	}
	for want, f := range cases {
		if got := WindowLabel(f); got != want {
			t.Errorf("WindowLabel = %q, want %q", got, want)
		}
	}
}

// groupedSource returns per-model rows when asked to group and their daily
// totals when not, as the event store does.
type groupedSource struct{ fakeSource }

func (g *groupedSource) AggregateBy(_ context.Context, flt Window, b analytics.Bucket, group analytics.Group) ([]Row, error) {
	g.calls = append(g.calls, flt)
	if b != analytics.BucketDay {
		return nil, nil
	}
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	if group == analytics.GroupNone {
		return []Row{
			{BucketStart: day(1), CostUSD: 3, TotalTokens: 30},
			{BucketStart: day(2), CostUSD: 5, TotalTokens: 50},
			{BucketStart: day(3), CostUSD: 4, TotalTokens: 40},
		}, nil
	}
	return []Row{
		{BucketStart: day(1), GroupKey: "a", CostUSD: 1, TotalTokens: 10},
		{BucketStart: day(1), GroupKey: "b", CostUSD: 2, TotalTokens: 20},
		{BucketStart: day(2), GroupKey: "a", CostUSD: 5, TotalTokens: 50},
		{BucketStart: day(3), GroupKey: "a", CostUSD: 1, TotalTokens: 10},
		{BucketStart: day(3), GroupKey: "b", CostUSD: 3, TotalTokens: 30},
	}, nil
}

// `tokenops spend --forecast` and the forecast tool answer the same
// question, so they project the same series: the daily totals, whatever
// the report is grouped by. Projecting the grouped rows fitted one point
// per model per day.
func TestSpendReportForecastMatchesTheForecastTool(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, group := range []analytics.Group{analytics.GroupModel, analytics.GroupNone} {
		src := &groupedSource{}
		got, err := SpendReport(context.Background(), src,
			ReportQuery{Group: group, Forecast: true, Horizon: 5}, now)
		if err != nil {
			t.Fatal(err)
		}
		want, err := ForecastSpend(context.Background(), src, 5, nil, "", now)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Forecast, want.Forecast) || !reflect.DeepEqual(got.ForecastTokens, want.ForecastTokens) {
			t.Errorf("group %q: report forecast %v differs from the tool's %v", group, got.Forecast, want.Forecast)
		}
	}
}

// A report bounded by --until forecasts from that bound, not from now.
func TestSpendReportForecastsFromTheGivenInstant(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	from := time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC)
	src := &groupedSource{}
	if _, err := SpendReport(context.Background(), src,
		ReportQuery{Forecast: true, ForecastFrom: from}, now); err != nil {
		t.Fatal(err)
	}
	last := src.calls[len(src.calls)-1]
	if want := from.Add(-forecastLookback); !last.Since.Equal(want) {
		t.Errorf("forecast history since %v, want %v", last.Since, want)
	}
}
