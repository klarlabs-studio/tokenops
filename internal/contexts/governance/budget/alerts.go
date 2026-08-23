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
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast"
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
	Name     string
	Window   Window
	LimitUSD float64
	// LimitTokens is the ceiling for a BasisTokens limit. Flat-rate
	// subscriptions bill $0.00 at the margin, so a dollar limit can
	// never trip for them — tokens are the quantity they actually
	// consume and the only budget they can meaningfully hold.
	LimitTokens int64
	WarnAt      float64
	CritAt      float64
	WorkflowID  string
	AgentID     string
	// Basis selects the metric the limit watches. The evaluator is
	// metric-agnostic — callers resolve the actual figure; this field
	// rides along so they know which one to fetch.
	Basis string
}

// Basis values for Limit.Basis.
const (
	BasisSpend      = "spend"
	BasisEquivalent = "equivalent"
	BasisTokens     = "tokens"
)

// TokenBased reports whether the limit is denominated in tokens rather
// than dollars.
func (l Limit) TokenBased() bool { return l.Basis == BasisTokens }

// Threshold returns the ceiling in the limit's own unit — tokens for
// BasisTokens, dollars otherwise. Zero or negative means the limit is
// unconfigured and inert.
func (l Limit) Threshold() float64 {
	if l.TokenBased() {
		return float64(l.LimitTokens)
	}
	return l.LimitUSD
}

// formatAmount renders v in the limit's unit. Token counts are integers
// with thousands separators; dollars keep two decimals.
func (l Limit) formatAmount(v float64) string {
	if l.TokenBased() {
		return withThousands(int64(v)) + " tokens"
	}
	return fmt.Sprintf("$%.2f", v)
}

// withThousands renders n with comma separators so nine-digit token
// counts stay readable in an alert line.
func withThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// WindowStart returns the UTC start of the calendar window containing
// now: midnight for daily, Monday midnight for weekly, first of the
// month for monthly. Unknown windows fall back to daily.
func WindowStart(w Window, now time.Time) time.Time {
	now = now.UTC()
	switch w {
	case WindowWeekly:
		day := now.Truncate(24 * time.Hour)
		offset := (int(day.Weekday()) + 6) % 7 // Monday = 0
		return day.AddDate(0, 0, -offset)
	case WindowMonthly:
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return now.Truncate(24 * time.Hour)
	}
}

// WindowEnd returns the exclusive end of the calendar window starting
// at start.
func WindowEnd(w Window, start time.Time) time.Time {
	switch w {
	case WindowWeekly:
		return start.AddDate(0, 0, 7)
	case WindowMonthly:
		return start.AddDate(0, 1, 0)
	default:
		return start.AddDate(0, 0, 1)
	}
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
	if limit.Threshold() <= 0 {
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
	ceiling := l.Threshold()
	if ceiling <= 0 {
		return Alert{}, false
	}
	frac := actual / ceiling
	severity := SeverityWarn
	switch {
	case frac >= l.CritAt:
		severity = SeverityCrit
	case frac >= l.WarnAt:
	default:
		return Alert{}, false
	}
	return Alert{
		Kind:      AlertThresholdReached,
		Severity:  severity,
		Limit:     l,
		ActualUSD: actual,
		Fraction:  frac,
		Message: fmt.Sprintf(
			"%s: spent %s of %s (%.0f%% of %s budget)",
			l.Name, l.formatAmount(actual), l.formatAmount(ceiling), frac*100, l.Window),
	}, true
}

func forecastAlert(l Limit, actual float64, fc []forecast.Prediction) (Alert, bool) {
	ceiling := l.Threshold()
	if ceiling <= 0 || len(fc) == 0 {
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
		if !breachFound && running >= ceiling {
			breachAt = p.At
			breachFound = true
		}
	}
	if !breachFound {
		return Alert{}, false
	}
	severity := SeverityWarn
	if projected >= ceiling*1.5 {
		severity = SeverityCrit
	}
	return Alert{
		Kind:         AlertForecastBreach,
		Severity:     severity,
		Limit:        l,
		ActualUSD:    actual,
		ProjectedUSD: projected,
		Fraction:     projected / ceiling,
		Message: fmt.Sprintf(
			"%s: projected %s vs %s %s limit; breach at %s",
			l.Name, l.formatAmount(projected), l.formatAmount(ceiling), l.Window,
			breachAt.Format(time.RFC3339)),
		BreachAt: breachAt,
	}, true
}
