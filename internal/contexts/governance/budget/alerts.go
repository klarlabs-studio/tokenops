// Package budget evaluates configured spend limits against the analytics
// rollup and a (optional) forecast, emitting Alerts that the CLI and
// dashboard surface to operators. Two alert kinds:
//
//   - threshold_reached: actual spend in the window has crossed a
//     percentage of the configured limit. Useful for "75% used" early
//     warnings.
//   - forecast_breach: forecasted spend at the end of the window
//     exceeds the configured limit. Drives "you are projected to blow
//     the weekly budget by Thursday" notices.
//
// The package is pure compute — it does no I/O. Callers wire it to the
// analytics aggregator and forecast engine, evaluate periodically, and
// route Alerts to whatever sink they prefer (CLI table, dashboard
// banner, OTLP exporter).
package budget

import (
	"fmt"
	"sort"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/forecast"
)

// Window identifies the budget cadence.
type Window string

// Known windows.
const (
	WindowDaily   Window = "daily"
	WindowWeekly  Window = "weekly"
	WindowMonthly Window = "monthly"
)

// Limit is a single budget rule. WarnAt and CritAt are fractional
// thresholds (0.0–1.0) of LimitUSD; the package emits the highest
// severity tripped per rule. Default WarnAt = 0.75, CritAt = 0.95 when
// zero.
type Limit struct {
	Name       string
	Window     Window
	LimitUSD   float64
	WarnAt     float64
	CritAt     float64
	WorkflowID string
	AgentID    string
}

// Severity ranks Alerts. Higher = louder.
type Severity int

// Severity values.
const (
	SeverityInfo Severity = iota
	SeverityWarn
	SeverityCrit
)

// String renders the severity for logs / CLI output.
func (s Severity) String() string {
	switch s {
	case SeverityCrit:
		return "critical"
	case SeverityWarn:
		return "warn"
	default:
		return "info"
	}
}

// AlertKind enumerates Alert.Kind.
type AlertKind string

// Known alert kinds.
const (
	AlertThresholdReached AlertKind = "threshold_reached"
	AlertForecastBreach   AlertKind = "forecast_breach"
)

// Alert is a budget finding.
type Alert struct {
	Kind         AlertKind
	Severity     Severity
	Limit        Limit
	ActualUSD    float64
	ProjectedUSD float64
	Fraction     float64
	Message      string
	// BreachAt is the predicted timestamp at which actual spend will
	// cross LimitUSD. Set only on forecast_breach alerts.
	BreachAt time.Time
}

// Evaluate evaluates limits against actualUSD (spend so far in the
// window) and forecast (predictions for the remainder). forecast may be
// nil — only threshold_reached alerts are then emitted.
func Evaluate(limit Limit, actualUSD float64, forecast []forecast.Prediction) []Alert {
	limit = applyLimitDefaults(limit)
	if limit.LimitUSD <= 0 {
		return nil
	}
	var alerts []Alert
	if a, ok := thresholdAlert(limit, actualUSD); ok {
		alerts = append(alerts, a)
	}
	if a, ok := forecastAlert(limit, actualUSD, forecast); ok {
		alerts = append(alerts, a)
	}
	for _, a := range alerts {
		publishExceeded(a)
	}
	return alerts
}

// EvaluateAll runs Evaluate over a slice of limits and concatenates the
// alerts (sorted by severity desc, then by Limit.Name).
func EvaluateAll(limits []Limit, actualBy func(Limit) float64, forecastBy func(Limit) []forecast.Prediction) []Alert {
	if actualBy == nil {
		return nil
	}
	var out []Alert
	for _, l := range limits {
		var fc []forecast.Prediction
		if forecastBy != nil {
			fc = forecastBy(l)
		}
		out = append(out, Evaluate(l, actualBy(l), fc)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity > out[j].Severity
		}
		return out[i].Limit.Name < out[j].Limit.Name
	})
	return out
}

func applyLimitDefaults(l Limit) Limit {
	if l.WarnAt <= 0 {
		l.WarnAt = 0.75
	}
	if l.CritAt <= 0 {
		l.CritAt = 0.95
	}
	if l.WarnAt > l.CritAt {
		l.WarnAt = l.CritAt
	}
	return l
}

func thresholdAlert(l Limit, actual float64) (Alert, bool) {
	if l.LimitUSD <= 0 {
		return Alert{}, false
	}
	frac := actual / l.LimitUSD
	switch {
	case frac >= l.CritAt:
		return Alert{
			Kind:      AlertThresholdReached,
			Severity:  SeverityCrit,
			Limit:     l,
			ActualUSD: actual,
			Fraction:  frac,
			Message: fmt.Sprintf(
				"%s: spent $%.2f of $%.2f (%.0f%% of %s budget)",
				l.Name, actual, l.LimitUSD, frac*100, l.Window),
		}, true
	case frac >= l.WarnAt:
		return Alert{
			Kind:      AlertThresholdReached,
			Severity:  SeverityWarn,
			Limit:     l,
			ActualUSD: actual,
			Fraction:  frac,
			Message: fmt.Sprintf(
				"%s: spent $%.2f of $%.2f (%.0f%% of %s budget)",
				l.Name, actual, l.LimitUSD, frac*100, l.Window),
		}, true
	}
	return Alert{}, false
}

func forecastAlert(l Limit, actual float64, fc []forecast.Prediction) (Alert, bool) {
	if l.LimitUSD <= 0 || len(fc) == 0 {
		return Alert{}, false
	}
	running := actual
	var (
		breachAt    time.Time
		projected   float64
		breachFound bool
	)
	for _, p := range fc {
		running += p.Value
		projected = running
		if !breachFound && running >= l.LimitUSD {
			breachAt = p.At
			breachFound = true
		}
	}
	if !breachFound {
		return Alert{}, false
	}
	severity := SeverityWarn
	if projected >= l.LimitUSD*1.5 {
		severity = SeverityCrit
	}
	return Alert{
		Kind:         AlertForecastBreach,
		Severity:     severity,
		Limit:        l,
		ActualUSD:    actual,
		ProjectedUSD: projected,
		Fraction:     projected / l.LimitUSD,
		Message: fmt.Sprintf(
			"%s: projected $%.2f vs $%.2f %s limit; breach at %s",
			l.Name, projected, l.LimitUSD, l.Window, breachAt.Format(time.RFC3339)),
		BreachAt: breachAt,
	}, true
}
