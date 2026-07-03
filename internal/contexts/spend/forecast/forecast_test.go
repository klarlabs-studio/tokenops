package forecast

import (
	"errors"
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

const eps = 1e-6

// All test fixtures use 1h intervals; mkSeries is parameterised on
// interval for readability rather than for variation.
func mkSeries(start time.Time, interval time.Duration, values ...float64) []Point {
	out := make([]Point, len(values))
	for i, v := range values {
		out[i] = Point{At: start.Add(interval * time.Duration(i)), Value: v}
	}
	return out
}

func TestLinearFitsKnownLine(t *testing.T) {
	// y = 10 + 5*x, no noise.
	hist := mkSeries(time.Unix(0, 0), time.Hour, 10, 15, 20, 25, 30)
	got, err := NewLinear().Forecast(hist, 3, time.Hour)
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	want := []float64{35, 40, 45}
	for i, p := range got {
		if math.Abs(p.Value-want[i]) > eps {
			t.Errorf("value[%d] = %f, want %f", i, p.Value, want[i])
		}
		// Zero-noise line should yield zero CI half-width.
		if math.Abs(p.Upper-p.Value) > 1e-9 {
			t.Errorf("expected zero CI on perfect fit, got upper=%f", p.Upper)
		}
	}
}

func TestLinearTimestampSpacing(t *testing.T) {
	start := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	hist := mkSeries(start, time.Hour, 1, 2, 3)
	got, _ := NewLinear().Forecast(hist, 4, time.Hour)
	for i, p := range got {
		want := start.Add(time.Hour * time.Duration(2+i+1))
		if !p.At.Equal(want) {
			t.Errorf("forecast[%d].At = %s, want %s", i, p.At, want)
		}
	}
}

func TestLinearInsufficientHistory(t *testing.T) {
	_, err := NewLinear().Forecast([]Point{{At: time.Now(), Value: 1}}, 1, time.Hour)
	if !errors.Is(err, ErrInsufficientHistory) {
		t.Errorf("err = %v", err)
	}
}

func TestLinearInvalidArgs(t *testing.T) {
	hist := mkSeries(time.Unix(0, 0), time.Hour, 1, 2, 3)
	if _, err := NewLinear().Forecast(hist, 0, time.Hour); err == nil {
		t.Error("expected horizon error")
	}
	if _, err := NewLinear().Forecast(hist, 3, 0); err == nil {
		t.Error("expected interval error")
	}
}

func TestLinearCIWidensWithNoise(t *testing.T) {
	clean := mkSeries(time.Unix(0, 0), time.Hour, 10, 20, 30, 40, 50)
	noisy := mkSeries(time.Unix(0, 0), time.Hour, 10, 22, 28, 41, 49)
	cleanFc, _ := NewLinear().Forecast(clean, 1, time.Hour)
	noisyFc, _ := NewLinear().Forecast(noisy, 1, time.Hour)
	cleanCI := cleanFc[0].Upper - cleanFc[0].Value
	noisyCI := noisyFc[0].Upper - noisyFc[0].Value
	if noisyCI <= cleanCI {
		t.Errorf("expected noisy CI > clean CI: clean=%f noisy=%f", cleanCI, noisyCI)
	}
}

func TestHoltCapturesTrend(t *testing.T) {
	// Linear-ish series; Holt should return approximately the next point.
	hist := mkSeries(time.Unix(0, 0), time.Hour, 100, 110, 120, 130, 140, 150)
	got, err := NewHolt(0.6, 0.4).Forecast(hist, 3, time.Hour)
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	// Allow generous tolerance — Holt smooths so values slightly lag.
	if got[0].Value < 145 || got[0].Value > 165 {
		t.Errorf("Holt next-step out of band: %f", got[0].Value)
	}
}

func TestHoltCIScalesWithHorizon(t *testing.T) {
	hist := mkSeries(time.Unix(0, 0), time.Hour, 100, 105, 99, 103, 110, 108, 115)
	got, err := NewHolt(0, 0).Forecast(hist, 5, time.Hour)
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	w0 := got[0].Upper - got[0].Value
	w4 := got[4].Upper - got[4].Value
	if w4 <= w0 {
		t.Errorf("Holt CI should widen with horizon: w0=%f w4=%f", w0, w4)
	}
}

func TestHoltDefaults(t *testing.T) {
	h := NewHolt(0, 0)
	if h.Alpha == 0 || h.Beta == 0 {
		t.Errorf("defaults not applied: %+v", h)
	}
	h2 := NewHolt(0.7, 0.2)
	if h2.Alpha != 0.7 || h2.Beta != 0.2 {
		t.Errorf("explicit values lost: %+v", h2)
	}
}

func TestSeriesFromRows(t *testing.T) {
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	rows := []analytics.Row{
		{BucketStart: now, TotalTokens: 100, CostUSD: 0.01},
		{BucketStart: now.Add(time.Hour), TotalTokens: 200, CostUSD: 0.02},
	}
	tok := SeriesFromRows(rows, TotalTokens)
	if len(tok) != 2 {
		t.Fatalf("len = %d", len(tok))
	}
	if tok[0].Value != 100 || tok[1].Value != 200 {
		t.Errorf("token series wrong: %+v", tok)
	}
	cost := SeriesFromRows(rows, CostUSD)
	if cost[0].Value != 0.01 || cost[1].Value != 0.02 {
		t.Errorf("cost series wrong: %+v", cost)
	}
	if SeriesFromRows(rows, nil) != nil {
		t.Error("nil accessor should return nil")
	}
}

func TestLinearFlatSeriesDegenerate(t *testing.T) {
	// All same value — slope = 0, predictions match the mean.
	hist := mkSeries(time.Unix(0, 0), time.Hour, 5, 5, 5, 5)
	got, err := NewLinear().Forecast(hist, 2, time.Hour)
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	for i, p := range got {
		if math.Abs(p.Value-5) > eps {
			t.Errorf("flat[%d] = %f, want 5", i, p.Value)
		}
	}
}

// Spend/token forecasts are non-negative quantities; a declining trend
// must clamp at zero instead of predicting negative spend (observed on
// real bursty agent data).
func TestForecastsClampAtZero(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	declining := []Point{
		{At: base, Value: 10},
		{At: base.Add(24 * time.Hour), Value: 6},
		{At: base.Add(48 * time.Hour), Value: 2},
		{At: base.Add(72 * time.Hour), Value: 0.5},
	}
	for name, f := range map[string]Forecaster{
		"holt":   NewHolt(0.6, 0.3),
		"linear": NewLinear(),
	} {
		preds, err := f.Forecast(declining, 7, 24*time.Hour)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, p := range preds {
			if p.Value < 0 || p.Lower < 0 || p.Upper < 0 {
				t.Errorf("%s: negative prediction %+v", name, p)
			}
		}
	}
}

// Clamping must preserve interval ordering and keep a positive upper
// bound when one exists — only fully-negative intervals collapse to a
// point mass at zero (truncated-distribution semantics).
func TestClampPreservesIntervalOrdering(t *testing.T) {
	preds := clampNonNegative([]Prediction{
		{Value: -2, Lower: -5, Upper: 1},  // upper tail positive → band survives
		{Value: -2, Lower: -5, Upper: -1}, // fully negative → point mass at 0
		{Value: 3, Lower: 1, Upper: 5},    // untouched
	})
	for i, p := range preds {
		if !(p.Lower <= p.Value && p.Value <= p.Upper) {
			t.Errorf("pred %d: ordering violated %+v", i, p)
		}
	}
	if preds[0].Upper != 1 {
		t.Errorf("positive upper bound clamped away: %+v", preds[0])
	}
	if preds[1].Upper != 0 || preds[1].Lower != 0 || preds[1].Value != 0 {
		t.Errorf("fully-negative interval should collapse to zero: %+v", preds[1])
	}
	if preds[2] != (Prediction{Value: 3, Lower: 1, Upper: 5}) {
		t.Errorf("non-negative prediction altered: %+v", preds[2])
	}
}
