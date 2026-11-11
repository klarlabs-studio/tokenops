package spending

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// forecastNow is a Saturday at noon, UTC.
var forecastNow = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

// dailyRows is a row on each of the n days before forecastNow's, the
// oldest at cost and each later day cost more.
func dailyRows(cost float64, n int) []analytics.Row {
	start := forecastNow.Truncate(24*time.Hour).AddDate(0, 0, -n)
	rows := make([]analytics.Row, n)
	for i := range rows {
		rows[i] = analytics.Row{BucketStart: start.AddDate(0, 0, i), CostUSD: cost * float64(i+1), TotalTokens: int64(1000 * (i + 1))}
	}
	return rows
}

func TestForecastSpendNeedsTwoDays(t *testing.T) {
	got, err := ForecastSpend(context.Background(), &fakeAgg{rows: dailyRows(1, 1)}, 3, nil, "USD", forecastNow)
	if err != nil {
		t.Fatal(err)
	}
	if got.Note != NoteInsufficientHistory || got.HistoryPoints != 1 || got.Forecast == nil || len(got.Forecast) != 0 || got.HorizonDays != 0 {
		t.Fatalf("got %+v", got)
	}
}

// Yesterday and today so far are one whole day, not two: today is still
// running.
func TestForecastSpendDoesNotCountTodayAsHistory(t *testing.T) {
	rows := append(dailyRows(1, 1), analytics.Row{BucketStart: forecastNow.Truncate(24 * time.Hour), CostUSD: 0.2})
	got, err := ForecastSpend(context.Background(), &fakeAgg{rows: rows}, 3, nil, "USD", forecastNow)
	if err != nil {
		t.Fatal(err)
	}
	if got.Note != NoteInsufficientHistory || got.HistoryPoints != 1 || len(got.Forecast) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestForecastSpendReadsThirtyDaysAndDefaultsTheHorizon(t *testing.T) {
	now := forecastNow
	agg := &fakeAgg{rows: dailyRows(2, 5)}
	got, err := ForecastSpend(context.Background(), agg, 0, []string{"mcp-session", "mcp-session"}, "EUR", now)
	if err != nil {
		t.Fatal(err)
	}
	if got.HorizonDays != 7 || len(got.Forecast) != 7 || len(got.ForecastTokens) != 7 || got.Currency != "EUR" || got.Note != "" || got.HistoryPoints != 5 {
		t.Fatalf("got %+v", got)
	}
	if !agg.got.Since.Equal(now.Add(-30*24*time.Hour)) || len(agg.got.IncludeSources) != 1 {
		t.Errorf("filter %+v", agg.got)
	}
	if first, tomorrow := got.Forecast[0].At, now.Truncate(24*time.Hour).AddDate(0, 0, 1); !first.Equal(tomorrow) {
		t.Errorf("the forecast starts at %v, want tomorrow (%v)", first, tomorrow)
	}
}

// A steady $20 a day stays $20 a day whatever the hour: the morning's
// dollar is not a day's spend, and a day off is a day at nothing.
func TestForecastSpendIsMadeFromWholeDays(t *testing.T) {
	today := forecastNow.Truncate(24 * time.Hour)
	// The read begins at noon 30 days ago: that day's afternoon.
	rows := []analytics.Row{{BucketStart: today.AddDate(0, 0, -30), CostUSD: 9, TotalTokens: 400}}
	for i := 29; i >= 1; i-- {
		rows = append(rows, analytics.Row{BucketStart: today.AddDate(0, 0, -i), CostUSD: 20, TotalTokens: 1000})
	}
	rows = append(rows, analytics.Row{BucketStart: today, CostUSD: 1, TotalTokens: 50})
	got, err := ForecastSpend(context.Background(), &fakeAgg{rows: rows}, 3, nil, "USD", forecastNow)
	if err != nil {
		t.Fatal(err)
	}
	if got.HistoryPoints != 29 || len(got.Forecast) != 3 {
		t.Fatalf("got %+v", got)
	}
	for i, p := range got.Forecast {
		if p.Value < 19.99 || p.Value > 20.01 {
			t.Errorf("day %d forecast $%.2f, want $20", i+1, p.Value)
		}
	}
	for i, p := range got.ForecastTokens {
		if p.Value < 999 || p.Value > 1001 {
			t.Errorf("day %d forecast %.0f tokens, want 1000", i+1, p.Value)
		}
	}
	report, err := Forecast(context.Background(), &fakeAgg{rows: rows}, 3, "USD", forecastNow)
	if err != nil {
		t.Fatal(err)
	}
	if report.HistoryPoints != 29 || len(report.History) != 31 || len(report.Forecast) != 3 || report.Forecast[0].Value < 19.99 {
		t.Errorf("the API's report = %d points, %d rows, forecast %+v", report.HistoryPoints, len(report.History), report.Forecast)
	}
}

// A plan-covered history projects a flat zero; the note says where the
// signal is instead.
func TestForecastSpendExplainsAnAllZeroSeries(t *testing.T) {
	got, err := ForecastSpend(context.Background(), &fakeAgg{rows: dailyRows(0, 4)}, 3, nil, "USD", forecastNow)
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

// A weekend on which nothing is ever spent forecasts zero as well. That
// history was billed, so it is not called plan-covered.
func TestForecastSpendDoesNotCallABilledHistoryPlanCovered(t *testing.T) {
	today := forecastNow.Truncate(24 * time.Hour) // a Saturday
	var rows []analytics.Row
	for i := 28; i >= 1; i-- {
		at := today.AddDate(0, 0, -i)
		if wd := at.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		rows = append(rows, analytics.Row{BucketStart: at, CostUSD: 5, TotalTokens: 1000})
	}
	// Asked on Saturday morning for one day ahead: Sunday.
	got, err := ForecastSpend(context.Background(), &fakeAgg{rows: rows}, 1, nil, "USD", forecastNow)
	if err != nil {
		t.Fatal(err)
	}
	// The series starts with the first Monday: 26 days.
	if got.HistoryPoints != 26 || !AllZero(got.Forecast) {
		t.Fatalf("fixture should forecast a Sunday at nothing from 26 days: %+v", got)
	}
	if got.Note != "" {
		t.Errorf("note = %q on a history that was billed", got.Note)
	}
}

// A forecast made as of an earlier time is made from the days before it:
// what was recorded since is what it is forecasting.
func TestForecastSpendAsOfAnEarlierTimeIgnoresLaterDays(t *testing.T) {
	today := forecastNow.Truncate(24 * time.Hour)
	rows := dailyRows(0, 5)
	for i := range rows {
		rows[i].CostUSD = 4
	}
	for i := range 3 {
		rows = append(rows, analytics.Row{BucketStart: today.AddDate(0, 0, i), CostUSD: 400, TotalTokens: 1})
	}
	got, err := ForecastSpend(context.Background(), &fakeAgg{rows: rows}, 2, nil, "USD", today)
	if err != nil {
		t.Fatal(err)
	}
	if got.HistoryPoints != 5 || len(got.Forecast) != 2 || got.Forecast[0].Value != 4 || !got.Forecast[0].At.Equal(today) {
		t.Fatalf("got %+v, want $4 a day from the five days before, starting with the day asked about", got)
	}
}
