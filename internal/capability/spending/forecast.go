package spending

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast"
)

// Prediction is one projected point with its 95% band.
type Prediction = forecast.Prediction

// defaultHorizonDays is how far a forecast looks when the caller says nothing.
const defaultHorizonDays = 7

// forecastHistory is how much daily history a spend forecast reads.
const forecastHistory = 30 * 24 * time.Hour

// Project forecasts daily rows horizon days ahead (seven when not
// positive), in dollars and in tokens. The token series is returned
// alongside because on a flat-rate plan the dollar history is zero at
// every point and its projection carries no signal.
func Project(rows []analytics.Row, horizon int) (cost, tokens []Prediction) {
	if horizon <= 0 {
		horizon = defaultHorizonDays
	}
	cost = forecast.AutoForecast(forecast.SeriesFromRows(rows, forecast.CostUSD), horizon, 24*time.Hour)
	tokens = forecast.AutoForecast(forecast.SeriesFromRows(rows, forecast.TotalTokens), horizon, 24*time.Hour)
	return cost, tokens
}

// AllZero reports whether a projection is entirely zero.
//
// A flat-rate plan's cost history is zero at every point, so the forecaster
// dutifully projects zero forward with zero-width bands. Shown bare that
// reads as a broken forecaster rather than as the correct answer to a
// question that does not apply, so every surface replaces it with a reason.
func AllZero(points []Prediction) bool {
	for _, p := range points {
		if p.Value != 0 || p.Lower != 0 || p.Upper != 0 {
			return false
		}
	}
	return true
}

// Forecast is the daily spend projection. Note is set when there is too
// little history to project, or when the dollar series is all zero.
type Forecast struct {
	HorizonDays   int          `json:"horizon_days,omitempty"`
	HistoryPoints int          `json:"history_points"`
	Forecast      []Prediction `json:"forecast"`
	// ForecastTokens projects token volume over the same horizon. On a
	// flat-rate plan the dollar forecast is a flat zero, so this is the
	// only series carrying signal — always returned alongside.
	ForecastTokens []Prediction `json:"forecast_tokens,omitempty"`
	Currency       string       `json:"currency,omitempty"`
	Note           string       `json:"note,omitempty"`
}

// Forecast notes.
const (
	NoteInsufficientHistory = "insufficient history (need ≥2 daily buckets)"
	NoteAllZero             = "cost history is all zero (plan-covered traffic bills $0 at the margin), so the dollar forecast is flat zero — forecast_tokens carries the signal"
)

// ForecastSpend projects daily spend horizon days ahead (seven when not
// positive) from the last thirty days.
func ForecastSpend(ctx context.Context, agg Aggregator, horizon int, include []string, currency string, now time.Time) (Forecast, error) {
	if horizon <= 0 {
		horizon = defaultHorizonDays
	}
	f := analytics.Filter{Since: now.Add(-forecastHistory), IncludeSources: dedupe(include)}
	rows, err := agg.AggregateBy(ctx, f, analytics.BucketDay, analytics.GroupNone)
	if err != nil {
		return Forecast{}, err
	}
	if len(rows) < 2 {
		return Forecast{
			HistoryPoints: len(rows),
			Forecast:      []Prediction{},
			Note:          NoteInsufficientHistory,
		}, nil
	}
	cost, tokens := Project(rows, horizon)
	out := Forecast{
		HorizonDays:    horizon,
		HistoryPoints:  len(rows),
		Forecast:       cost,
		ForecastTokens: tokens,
		Currency:       currency,
	}
	if AllZero(cost) {
		out.Note = NoteAllZero
	}
	return out, nil
}
