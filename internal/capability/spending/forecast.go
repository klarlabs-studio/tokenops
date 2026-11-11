package spending

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast"
)

// Prediction is one projected point with its 95% band.
type Prediction = forecast.Prediction

// Projection is a daily forecast in dollars and in tokens. The token
// series is there because on a flat-rate plan the dollar history is zero
// on every day and its projection carries no signal.
type Projection struct {
	Cost, Tokens []Prediction
	// Days is how many whole days the forecast was made from.
	Days int
	// Unbilled is true when none of those days cost anything.
	Unbilled bool
}

// Project forecasts the horizon whole days ahead of now (seven when not
// positive) from daily rows read since `since`. It is made from whole
// days only, a day with no rows counting as a day at zero, and expects of
// a day what its weekday has brought (forecast.Daily).
func Project(rows []analytics.Row, horizon int, since, now time.Time) Projection {
	if horizon <= 0 {
		horizon = defaultHorizonDays
	}
	spend := forecast.DailyFrom(rows, forecast.CostUSD, since, now)
	return Projection{
		Cost:     spend.Ahead(horizon),
		Tokens:   forecast.DailyFrom(rows, forecast.TotalTokens, since, now).Ahead(horizon),
		Days:     len(spend.Points),
		Unbilled: spend.Zero(),
	}
}

// AllZero reports whether a projection is entirely zero.
//
// A flat-rate plan's cost history is zero on every day, so its projection
// is zero with a zero-width band. Shown bare that reads as a broken
// forecaster rather than as the correct answer to a question that does
// not apply, so every surface replaces it with the reason (NoteAllZero).
func AllZero(points []Prediction) bool {
	for _, p := range points {
		if p.Value != 0 || p.Lower != 0 || p.Upper != 0 {
			return false
		}
	}
	return true
}

// SpendForecast is the agent's daily spend projection. Note is set when
// there is too little history to project, or when no day of the history
// cost anything. The daemon API's ForecastReport projects the same series with
// the same Project; it carries the history instead of a note.
type SpendForecast struct {
	HorizonDays int `json:"horizon_days,omitempty"`
	// HistoryPoints is the whole days the projection was made from:
	// today, still running, is not one of them.
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
	NoteInsufficientHistory = "insufficient history (need ≥2 whole days before today)"
	NoteAllZero             = "cost history is all zero (plan-covered traffic bills $0 at the margin), so the dollar forecast is flat zero — forecast_tokens carries the signal"
)

// ForecastSpend projects daily spend horizon days ahead (seven when not
// positive) from the last thirty days.
func ForecastSpend(ctx context.Context, agg Aggregator, horizon int, include []string, currency string, now time.Time) (SpendForecast, error) {
	if horizon <= 0 {
		horizon = defaultHorizonDays
	}
	f := Window{Since: now.Add(-forecastLookback), IncludeSources: dedupe(include)}
	rows, err := agg.AggregateBy(ctx, f, analytics.BucketDay, analytics.GroupNone)
	if err != nil {
		return SpendForecast{}, err
	}
	p := Project(rows, horizon, f.Since, now)
	if p.Days < forecast.MinDays {
		return SpendForecast{
			HistoryPoints: p.Days,
			Forecast:      []Prediction{},
			Note:          NoteInsufficientHistory,
		}, nil
	}
	out := SpendForecast{
		HorizonDays:    horizon,
		HistoryPoints:  p.Days,
		Forecast:       p.Cost,
		ForecastTokens: p.Tokens,
		Currency:       currency,
	}
	// The note states a fact about the history, so it is set from the
	// history: a zero forecast alone can also be days of the week on
	// which nothing is ever spent.
	if p.Unbilled {
		out.Note = NoteAllZero
	}
	return out, nil
}
