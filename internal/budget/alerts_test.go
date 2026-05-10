package budget

import (
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/forecast"
)

func TestThresholdWarnAndCrit(t *testing.T) {
	l := Limit{Name: "weekly-eng", Window: WindowWeekly, LimitUSD: 100}
	if alerts := Evaluate(l, 50, nil); len(alerts) != 0 {
		t.Errorf("50%% should not alert: %+v", alerts)
	}
	if alerts := Evaluate(l, 80, nil); len(alerts) != 1 || alerts[0].Severity != SeverityWarn {
		t.Errorf("80%% should warn: %+v", alerts)
	}
	if alerts := Evaluate(l, 96, nil); len(alerts) != 1 || alerts[0].Severity != SeverityCrit {
		t.Errorf("96%% should crit: %+v", alerts)
	}
}

func TestZeroLimitNoOp(t *testing.T) {
	l := Limit{Name: "z", LimitUSD: 0}
	if alerts := Evaluate(l, 100, nil); len(alerts) != 0 {
		t.Errorf("zero limit should produce no alerts: %+v", alerts)
	}
}

func TestForecastBreachAlert(t *testing.T) {
	l := Limit{Name: "weekly", Window: WindowWeekly, LimitUSD: 100}
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	fc := []forecast.Prediction{
		{At: now.Add(time.Hour), Value: 30},
		{At: now.Add(2 * time.Hour), Value: 30},
		{At: now.Add(3 * time.Hour), Value: 30},
		{At: now.Add(4 * time.Hour), Value: 30}, // running cumulative crosses 100 here
	}
	alerts := Evaluate(l, 20, fc)
	hasBreach := false
	for _, a := range alerts {
		if a.Kind == AlertForecastBreach {
			hasBreach = true
			if a.BreachAt.IsZero() {
				t.Errorf("BreachAt unset: %+v", a)
			}
			if a.ProjectedUSD < 100 {
				t.Errorf("projected < limit: %f", a.ProjectedUSD)
			}
		}
	}
	if !hasBreach {
		t.Errorf("expected forecast breach: %+v", alerts)
	}
}

func TestForecastNoBreachWhenUnderLimit(t *testing.T) {
	l := Limit{Name: "weekly", Window: WindowWeekly, LimitUSD: 100}
	now := time.Now().UTC()
	fc := []forecast.Prediction{
		{At: now, Value: 5}, {At: now.Add(time.Hour), Value: 5},
	}
	alerts := Evaluate(l, 10, fc)
	for _, a := range alerts {
		if a.Kind == AlertForecastBreach {
			t.Errorf("unexpected breach: %+v", a)
		}
	}
}

func TestForecastCriticalWhen150Pct(t *testing.T) {
	l := Limit{Name: "weekly", Window: WindowWeekly, LimitUSD: 100}
	fc := []forecast.Prediction{
		{Value: 200},
	}
	alerts := Evaluate(l, 0, fc)
	found := false
	for _, a := range alerts {
		if a.Kind == AlertForecastBreach && a.Severity == SeverityCrit {
			found = true
		}
	}
	if !found {
		t.Errorf("expected critical forecast breach, got %+v", alerts)
	}
}

func TestEvaluateAllSortsBySeverity(t *testing.T) {
	limits := []Limit{
		{Name: "a", Window: WindowDaily, LimitUSD: 100},
		{Name: "b", Window: WindowDaily, LimitUSD: 100},
	}
	actuals := map[string]float64{"a": 80, "b": 99}
	got := EvaluateAll(limits,
		func(l Limit) float64 { return actuals[l.Name] }, nil)
	if len(got) != 2 {
		t.Fatalf("got %d alerts, want 2", len(got))
	}
	if got[0].Severity != SeverityCrit || got[1].Severity != SeverityWarn {
		t.Errorf("not sorted by severity desc: %+v", got)
	}
	if got[0].Limit.Name != "b" {
		t.Errorf("crit alert should be b (99/100): %+v", got[0])
	}
}

func TestEvaluateAllNilActualByReturnsNil(t *testing.T) {
	if got := EvaluateAll([]Limit{{Name: "x", LimitUSD: 100}}, nil, nil); got != nil {
		t.Errorf("nil accessor: %+v", got)
	}
}

func TestSeverityString(t *testing.T) {
	cases := []struct {
		s    Severity
		want string
	}{
		{SeverityInfo, "info"},
		{SeverityWarn, "warn"},
		{SeverityCrit, "critical"},
	}
	for _, tc := range cases {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("Severity %d = %q, want %q", tc.s, got, tc.want)
		}
	}
}

func TestDefaultsApplied(t *testing.T) {
	l := Limit{Name: "x", Window: WindowDaily, LimitUSD: 100}
	// 75% should warn at default threshold; 95% should crit.
	if alerts := Evaluate(l, 76, nil); len(alerts) == 0 || alerts[0].Severity != SeverityWarn {
		t.Errorf("default WarnAt=0.75 not applied: %+v", alerts)
	}
	if alerts := Evaluate(l, 96, nil); len(alerts) == 0 || alerts[0].Severity != SeverityCrit {
		t.Errorf("default CritAt=0.95 not applied: %+v", alerts)
	}
}

func TestMessageContainsContext(t *testing.T) {
	l := Limit{Name: "weekly-eng", Window: WindowWeekly, LimitUSD: 100}
	alerts := Evaluate(l, 80, nil)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert")
	}
	msg := alerts[0].Message
	for _, want := range []string{"weekly-eng", "$80.00", "$100.00", "weekly"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}
}
