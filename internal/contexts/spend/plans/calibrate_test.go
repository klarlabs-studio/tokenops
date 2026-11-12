package plans

import (
	"math"
	"reflect"
	"strconv"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A vendor reports a window as a share, never in tokens. How many tokens
// one percent of it held is measured from the operator's own windows: the
// tokens used while each one was open against how full it got. The longest
// window kind is the one a unit of work is compared with.
func TestCalibrateMeasuresTokensPerPercentOfTheLongestWindow(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	since := now.Add(-35 * 24 * time.Hour)
	weekly := func(reset time.Time, used float64) QuotaWindow {
		return QuotaWindow{Label: "weekly", UsedPct: used, ResetsAt: reset, Duration: week}
	}
	w1, w2, w3 := now.Add(-20*24*time.Hour), now.Add(-13*24*time.Hour), now.Add(-6*24*time.Hour)
	ws := []QuotaWindow{
		weekly(w1, 10), weekly(w1, 40),
		weekly(w2, 50),
		weekly(w3, 2),                     // too empty to measure: one turn would swing it
		weekly(now.Add(24*time.Hour), 80), // still open
		{Label: "5-hour", UsedPct: 90, ResetsAt: now.Add(-30 * time.Hour), Duration: 5 * time.Hour}, // shorter kind
	}
	tokens := map[time.Time]int64{w1: 4_000_000, w2: 6_000_000, w3: 1_000}
	tokensIn := func(from, to time.Time) int64 {
		if to.Sub(from) != week {
			t.Errorf("tokens asked over %v, want the weekly window", to.Sub(from))
		}
		return tokens[to]
	}
	c, ok := Calibrate("anthropic", ws, since, now, tokensIn)
	if !ok {
		t.Fatal("no calibration")
	}
	// 4M/40% = 100k per %, 6M/50% = 120k per %; the median of two is
	// their mean.
	if c.Provider != "anthropic" || c.Window != "weekly" || c.Windows != 2 || math.Abs(c.TokensPerPct-110_000) > 1e-6 {
		t.Fatalf("calibration = %+v", c)
	}
	if got := c.Share(55_000); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("Share(55k) = %v, want 0.5%% of a weekly window", got)
	}
}

// No window that reset with enough in it, or no tokens recorded while it
// was open: nothing to measure with, and no share is claimed.
func TestCalibrateWithoutMeasurableWindowsIsAbsent(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	since := now.Add(-35 * 24 * time.Hour)
	none := func(time.Time, time.Time) int64 { return 0 }
	some := func(time.Time, time.Time) int64 { return 1_000 }
	for name, tc := range map[string]struct {
		ws       []QuotaWindow
		tokensIn func(time.Time, time.Time) int64
	}{
		"no readings":       {nil, some},
		"only open windows": {[]QuotaWindow{{Label: "weekly", UsedPct: 50, ResetsAt: now.Add(time.Hour), Duration: week}}, some},
		"too empty":         {[]QuotaWindow{{Label: "weekly", UsedPct: 1, ResetsAt: now.Add(-time.Hour), Duration: week}}, some},
		"no tokens":         {[]QuotaWindow{{Label: "weekly", UsedPct: 50, ResetsAt: now.Add(-time.Hour), Duration: week}}, none},
		"no length":         {[]QuotaWindow{{Label: "weekly", UsedPct: 50, ResetsAt: now.Add(-time.Hour)}}, some},
	} {
		if c, ok := Calibrate("anthropic", tc.ws, since, now, tc.tokensIn); ok {
			t.Errorf("%s: calibration = %+v, want none", name, c)
		}
	}
}

func TestWindowReadingsCollectsEachProvidersWindows(t *testing.T) {
	reset := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	envs := []*eventschema.Envelope{
		{Source: "claude-usage-meter", Attributes: map[string]string{
			"seven_day_kind": "weekly_all", "seven_day_used_pct": "40", "seven_day_reset_at": reset,
		}},
		{Source: "claude-code-jsonl", Attributes: map[string]string{"seven_day_used_pct": "99"}},
		nil,
	}
	got := WindowReadings(envs)
	if len(got) != 1 || len(got["anthropic"]) != 1 || got["anthropic"][0].UsedPct != 40 {
		t.Fatalf("readings = %+v", got)
	}
}

// Window peaks keep one reading per window, at its peak: a meter that
// reads every turn records the same window thousands of times, and the
// calibration over the peaks is the calibration over every reading.
func TestWindowPeaksKeepEachWindowsPeak(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	codex := func(pct float64, reset time.Time) *eventschema.Envelope {
		return &eventschema.Envelope{Source: "codex-jsonl", Attributes: map[string]string{
			"primary_used_pct": strconv.FormatFloat(pct, 'f', -1, 64), "primary_window_min": "10080",
			"primary_resets_at": strconv.FormatInt(reset.Unix(), 10),
		}}
	}
	first, second := now.Add(-14*24*time.Hour), now.Add(-7*24*time.Hour)
	envs := make([]*eventschema.Envelope, 0, 2000)
	for i := range 1000 {
		envs = append(envs, codex(float64(i%60), first), codex(float64(i%80), second))
	}
	var peaks WindowPeaks
	for _, e := range envs {
		peaks.Add(e)
	}
	got := peaks.Readings()["openai"]
	want := []QuotaWindow{
		{Label: "weekly", UsedPct: 59, ResetsAt: first.UTC(), Duration: week},
		{Label: "weekly", UsedPct: 79, ResetsAt: second.UTC(), Duration: week},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("peaks = %+v, want %+v", got, want)
	}
	tokensIn := func(from, to time.Time) int64 { return to.Sub(from).Milliseconds() }
	all := WindowReadings(nil)
	for _, e := range envs {
		all["openai"] = append(all["openai"], QuotaWindowsFromAttributes(eventschema.ProviderOpenAI, e.Attributes)...)
	}
	since := now.Add(-28 * 24 * time.Hour)
	fromAll, okAll := Calibrate("openai", all["openai"], since, now, tokensIn)
	fromPeaks, okPeaks := Calibrate("openai", got, since, now, tokensIn)
	if !okAll || fromAll != fromPeaks || okAll != okPeaks {
		t.Errorf("calibration over peaks = %+v (%v), over every reading = %+v (%v)", fromPeaks, okPeaks, fromAll, okAll)
	}
}
