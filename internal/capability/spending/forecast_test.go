package spending

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

func dailyRows(cost float64, n int) []analytics.Row {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]analytics.Row, n)
	for i := range rows {
		rows[i] = analytics.Row{BucketStart: start.AddDate(0, 0, i), CostUSD: cost * float64(i+1), TotalTokens: int64(1000 * (i + 1))}
	}
	return rows
}

func TestForecastSpendNeedsTwoDays(t *testing.T) {
	got, err := ForecastSpend(context.Background(), &fakeAgg{rows: dailyRows(1, 1)}, 3, nil, "USD", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.Note != NoteInsufficientHistory || got.HistoryPoints != 1 || got.Forecast == nil || len(got.Forecast) != 0 || got.HorizonDays != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestForecastSpendReadsThirtyDaysAndDefaultsTheHorizon(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	agg := &fakeAgg{rows: dailyRows(2, 5)}
	got, err := ForecastSpend(context.Background(), agg, 0, []string{"mcp-session", "mcp-session"}, "EUR", now)
	if err != nil {
		t.Fatal(err)
	}
	if got.HorizonDays != 7 || len(got.Forecast) != 7 || len(got.ForecastTokens) != 7 || got.Currency != "EUR" || got.Note != "" {
		t.Fatalf("got %+v", got)
	}
	if !agg.got.Since.Equal(now.Add(-30*24*time.Hour)) || len(agg.got.IncludeSources) != 1 {
		t.Errorf("filter %+v", agg.got)
	}
}

// A plan-covered history projects a flat zero; the note says where the
// signal is instead.
func TestForecastSpendExplainsAnAllZeroSeries(t *testing.T) {
	got, err := ForecastSpend(context.Background(), &fakeAgg{rows: dailyRows(0, 4)}, 3, nil, "USD", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !AllZero(got.Forecast) || got.Note != NoteAllZero {
		t.Fatalf("got %+v", got)
	}
	if AllZero(got.ForecastTokens) {
		t.Errorf("token forecast should carry the signal: %+v", got.ForecastTokens)
	}
}
