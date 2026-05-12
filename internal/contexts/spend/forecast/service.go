package forecast

import (
	"time"
)

// AutoForecast applies the model-selection rule TokenOps uses across every
// surface: Holt's exponential smoothing when at least 4 history points
// are available, simple linear regression for 2–3 points, and an empty
// prediction set otherwise. Centralising this here means HTTP handlers,
// MCP tools, and the CLI never reinvent the heuristic.
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
