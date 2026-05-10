// Package anomaly detects spike anomalies in time-bucketed token /
// spend series. The implementation is the standard z-score test
// against a rolling baseline: a point's z-score exceeding the
// configured threshold is flagged as an Anomaly with severity scaled
// to its z-score.
//
// Two windowing strategies are exposed:
//
//   - DetectFixed uses a single mean+stddev computed over the entire
//     supplied series. Cheap, suitable for short windows.
//   - DetectRolling uses a sliding baseline (the N points before each
//     candidate) so the detector adapts to shifting volume regimes
//     without hand-resetting.
package anomaly

import (
	"errors"
	"math"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/forecast"
)

// Severity ranks anomalies for downstream alert routing.
type Severity int

// Severity values.
const (
	SeverityNone Severity = iota
	SeverityLow
	SeverityMedium
	SeverityHigh
)

// Anomaly is one detected spike.
type Anomaly struct {
	At        time.Time
	Value     float64
	Baseline  float64
	StdDev    float64
	ZScore    float64
	Severity  Severity
	Direction string // "spike" | "dip"
}

// Config tunes the detector. Zero values produce defaults: WindowSize=12,
// Threshold=3.0 (3-sigma), MinPoints=4 (need at least 4 baseline samples
// before scoring).
type Config struct {
	WindowSize int
	Threshold  float64
	MinPoints  int
	// IncludeDips, when true, flags negative deviations (sudden drops)
	// in addition to positive spikes. Default false — token spikes are
	// the cost concern; drops are usually harmless.
	IncludeDips bool
}

func (c *Config) defaults() {
	if c.WindowSize <= 0 {
		c.WindowSize = 12
	}
	if c.Threshold <= 0 {
		c.Threshold = 3.0
	}
	if c.MinPoints <= 0 {
		c.MinPoints = 4
	}
}

// ErrInsufficient is returned by Detect when fewer than MinPoints
// samples are supplied.
var ErrInsufficient = errors.New("anomaly: insufficient samples")

// DetectFixed scores every point against a baseline computed over the
// full series. Useful for one-shot reports.
func DetectFixed(series []forecast.Point, cfg Config) ([]Anomaly, error) {
	cfg.defaults()
	if len(series) < cfg.MinPoints {
		return nil, ErrInsufficient
	}
	mean, std := meanStd(values(series))
	var out []Anomaly
	for _, p := range series {
		if a, ok := score(p, mean, std, cfg); ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// DetectRolling uses a sliding baseline: each point is scored against
// the WindowSize points immediately preceding it.
func DetectRolling(series []forecast.Point, cfg Config) ([]Anomaly, error) {
	cfg.defaults()
	if len(series) < cfg.MinPoints+1 {
		return nil, ErrInsufficient
	}
	var out []Anomaly
	for i := cfg.MinPoints; i < len(series); i++ {
		start := i - cfg.WindowSize
		if start < 0 {
			start = 0
		}
		baseline := values(series[start:i])
		if len(baseline) < cfg.MinPoints {
			continue
		}
		mean, std := meanStd(baseline)
		if a, ok := score(series[i], mean, std, cfg); ok {
			out = append(out, a)
		}
	}
	return out, nil
}

func score(p forecast.Point, mean, std float64, cfg Config) (Anomaly, bool) {
	if std == 0 {
		// Constant baseline: treat any change as an anomaly only when
		// it crosses an absolute threshold. We pick mean*0.5 — so a
		// jump from 100 to >150 fires; smaller fluctuations stay quiet.
		if p.Value <= mean*1.5 && p.Value >= mean*0.5 {
			return Anomaly{}, false
		}
	}
	z := 0.0
	if std > 0 {
		z = (p.Value - mean) / std
	}
	abs := math.Abs(z)
	if abs < cfg.Threshold {
		// On constant baselines (std=0) the threshold check above is
		// already binary; emit the anomaly here.
		if std > 0 {
			return Anomaly{}, false
		}
	}
	if z < 0 && !cfg.IncludeDips {
		return Anomaly{}, false
	}
	dir := "spike"
	if z < 0 {
		dir = "dip"
	}
	return Anomaly{
		At:        p.At,
		Value:     p.Value,
		Baseline:  mean,
		StdDev:    std,
		ZScore:    z,
		Severity:  severityFor(abs, cfg.Threshold),
		Direction: dir,
	}, true
}

func severityFor(absZ, threshold float64) Severity {
	switch {
	case absZ >= threshold*2:
		return SeverityHigh
	case absZ >= threshold*1.5:
		return SeverityMedium
	case absZ >= threshold:
		return SeverityLow
	default:
		return SeverityNone
	}
}

func values(ps []forecast.Point) []float64 {
	out := make([]float64, len(ps))
	for i, p := range ps {
		out[i] = p.Value
	}
	return out
}

func meanStd(xs []float64) (mean, std float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean = sum / float64(len(xs))
	if len(xs) < 2 {
		return mean, 0
	}
	var sq float64
	for _, x := range xs {
		d := x - mean
		sq += d * d
	}
	std = math.Sqrt(sq / float64(len(xs)-1))
	return mean, std
}
