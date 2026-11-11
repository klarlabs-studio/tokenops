package forecast

import (
	"time"
)

// AutoForecast picks the trend method for a series by its length: Holt's
// exponential smoothing when at least 4 history points are available,
// simple linear regression for 2–3 points, and an empty prediction set
// otherwise. A daily spend series is forecast by Daily instead: a trend
// through the last few days reads a quiet weekend as a fall to nothing.
func AutoForecast(history []Point, horizon int, interval time.Duration) []Prediction {
	switch {
	case len(history) >= 4:
		preds, _ := NewHolt(0.6, 0.3).Forecast(history, horizon, interval)
		return preds
	case len(history) >= 2:
		preds, _ := NewLinear().Forecast(history, horizon, interval)
		return preds
	default:
		return nil
	}
}
