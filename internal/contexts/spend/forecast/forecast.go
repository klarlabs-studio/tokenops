// Package forecast provides token-consumption and spend forecasters
// over the time-bucketed series produced by internal/analytics. Two
// methods are implemented:
//
//   - LinearForecaster: ordinary least squares on (t, y) — captures a
//     linear trend, fast, and gives the best signal when the series is
//     short or roughly monotonic.
//
//   - HoltForecaster: Holt's double-exponential smoothing (level +
//     trend) — handles series with a changing slope and is the default
//     for the dashboard's weekly forecast.
//
// Confidence intervals are computed from the residual standard error
// using the normal-approximation factor (1.96 for 95% CI).
package forecast

import (
	"errors"
	"math"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/observability/analytics"
)

// Point is one (timestamp, value) sample. Forecasters consume series of
// these and emit forecasts in the same shape.
type Point struct {
	At    time.Time
	Value float64
}

// Prediction is one predicted value with a 95% confidence interval.
// Lower / Upper bracket Value at the level computed from residual
// standard error.
type Prediction struct {
	At    time.Time
	Value float64
	Lower float64
	Upper float64
}

// Forecaster is the common interface. Forecast returns predictions for
// the next horizon points at interval cadence.
type Forecaster interface {
	Forecast(history []Point, horizon int, interval time.Duration) ([]Prediction, error)
}

// SeriesFromRows extracts (BucketStart, value) points from analytics rows
// using the supplied accessor. The returned series is in the row order;
// callers are expected to sort or pre-bucket as needed.
func SeriesFromRows(rows []analytics.Row, get func(analytics.Row) float64) []Point {
	if get == nil {
		return nil
	}
	out := make([]Point, len(rows))
	for i, r := range rows {
		out[i] = Point{At: r.BucketStart, Value: get(r)}
	}
	return out
}

// TotalTokens returns the rollup token total accessor for SeriesFromRows.
func TotalTokens(r analytics.Row) float64 { return float64(r.TotalTokens) }

// CostUSD returns the rollup spend accessor for SeriesFromRows.
func CostUSD(r analytics.Row) float64 { return r.CostUSD }

// ConfidenceZ95 is the 1-sided 1.96 normal-approximation factor for a
// 95% CI. Exposed so callers can reuse it for non-Gaussian variants.
const ConfidenceZ95 = 1.96

// ErrInsufficientHistory is returned by Forecast when the series has
// too few points for the chosen method.
var ErrInsufficientHistory = errors.New("forecast: insufficient history")

// --- Linear regression ----------------------------------------------------

// LinearForecaster fits y = a + b*t via OLS over the supplied history.
type LinearForecaster struct{}

// NewLinear returns a LinearForecaster.
func NewLinear() *LinearForecaster { return &LinearForecaster{} }

// Forecast emits horizon points spaced by interval.
func (LinearForecaster) Forecast(history []Point, horizon int, interval time.Duration) ([]Prediction, error) {
	if len(history) < 2 {
		return nil, ErrInsufficientHistory
	}
	if horizon <= 0 {
		return nil, errors.New("forecast: horizon must be positive")
	}
	if interval <= 0 {
		return nil, errors.New("forecast: interval must be positive")
	}

	// Use index-as-x to avoid time-domain blow-up; convert back at end.
	a, b, sigma := linearFit(history)
	last := history[len(history)-1].At

	out := make([]Prediction, horizon)
	for i := 0; i < horizon; i++ {
		x := float64(len(history) + i)
		y := a + b*x
		ci := ConfidenceZ95 * sigma
		out[i] = Prediction{
			At:    last.Add(interval * time.Duration(i+1)),
			Value: y,
			Lower: y - ci,
			Upper: y + ci,
		}
	}
	return out, nil
}

func linearFit(history []Point) (a, b, residualStdErr float64) {
	n := float64(len(history))
	var sumX, sumY, sumXY, sumX2 float64
	for i, p := range history {
		x := float64(i)
		sumX += x
		sumY += p.Value
		sumXY += x * p.Value
		sumX2 += x * x
	}
	mean := sumY / n
	denom := n*sumX2 - sumX*sumX
	if denom == 0 {
		// All x identical — fall back to constant-mean forecast.
		return mean, 0, residualSigma(history, mean, 0)
	}
	b = (n*sumXY - sumX*sumY) / denom
	a = (sumY - b*sumX) / n
	return a, b, residualSigma(history, a, b)
}

func residualSigma(history []Point, a, b float64) float64 {
	if len(history) <= 2 {
		return 0
	}
	var ss float64
	for i, p := range history {
		pred := a + b*float64(i)
		diff := p.Value - pred
		ss += diff * diff
	}
	return math.Sqrt(ss / float64(len(history)-2))
}

// --- Holt's method --------------------------------------------------------

// HoltForecaster implements double exponential smoothing with a
// configurable smoothing factor (alpha) and trend factor (beta).
type HoltForecaster struct {
	Alpha float64
	Beta  float64
}

// NewHolt returns a Holt forecaster. alpha and beta must be in (0, 1];
// values <=0 fall back to alpha=0.5, beta=0.3 (rule-of-thumb defaults).
func NewHolt(alpha, beta float64) *HoltForecaster {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.5
	}
	if beta <= 0 || beta > 1 {
		beta = 0.3
	}
	return &HoltForecaster{Alpha: alpha, Beta: beta}
}

// Forecast emits horizon points spaced by interval.
func (h HoltForecaster) Forecast(history []Point, horizon int, interval time.Duration) ([]Prediction, error) {
	if len(history) < 2 {
		return nil, ErrInsufficientHistory
	}
	if horizon <= 0 {
		return nil, errors.New("forecast: horizon must be positive")
	}
	if interval <= 0 {
		return nil, errors.New("forecast: interval must be positive")
	}

	level := history[0].Value
	trend := history[1].Value - history[0].Value

	var ssRes float64
	for i := 1; i < len(history); i++ {
		pred := level + trend
		ssRes += (history[i].Value - pred) * (history[i].Value - pred)
		newLevel := h.Alpha*history[i].Value + (1-h.Alpha)*(level+trend)
		newTrend := h.Beta*(newLevel-level) + (1-h.Beta)*trend
		level, trend = newLevel, newTrend
	}
	sigma := 0.0
	if len(history) > 2 {
		sigma = math.Sqrt(ssRes / float64(len(history)-2))
	}

	last := history[len(history)-1].At
	out := make([]Prediction, horizon)
	for i := 0; i < horizon; i++ {
		y := level + trend*float64(i+1)
		// CI widens with the square root of the horizon — a small
		// approximation but standard for exponential-smoothing methods.
		ci := ConfidenceZ95 * sigma * math.Sqrt(float64(i+1))
		out[i] = Prediction{
			At:    last.Add(interval * time.Duration(i+1)),
			Value: y,
			Lower: y - ci,
			Upper: y + ci,
		}
	}
	return out, nil
}
