package compactwatch

import (
	"math"
	"testing"
	"time"
)

var setting = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// weeks builds one week per value, starting a week before the setting for
// each before value and at the setting for each after value.
func weeks(oversized []int64, rejects []float64) []Week {
	out := make([]Week, 0, len(oversized))
	for i := range oversized {
		out = append(out, Week{Start: setting.AddDate(0, 0, 7*(i-len(oversized)+1)),
			OversizedTokens: oversized[i], RejectRatePct: rejects[i]})
	}
	return out
}

// series joins a baseline and the weeks after the setting into one run,
// where the setting falls between the two.
func series(before, after []Week) []Week {
	out := make([]Week, 0, len(before)+len(after))
	for i, w := range before {
		w.Start = setting.AddDate(0, 0, -7*(len(before)-i))
		out = append(out, w)
	}
	for i, w := range after {
		w.Start = setting.AddDate(0, 0, 7*i)
		out = append(out, w)
	}
	return out
}

func TestJudgeCallsARealDropRealized(t *testing.T) {
	tests := []struct {
		name          string
		before, after []Week
		want          bool
	}{
		{
			name:   "a steady baseline and a drop well beyond its spread",
			before: weeks([]int64{900_000, 910_000, 905_000, 895_000}, []float64{14, 15, 14, 15}),
			after:  weeks([]int64{300_000, 280_000, 310_000, 290_000}, []float64{8, 7, 8, 7}),
			want:   true,
		},
		{
			name:   "the same drop on a baseline that swings as much as the drop",
			before: weeks([]int64{600_000, 1_200_000, 500_000, 1_100_000}, []float64{14, 15, 14, 15}),
			after:  weeks([]int64{550_000, 560_000, 540_000, 555_000}, []float64{8, 7, 8, 7}),
			want:   false,
		},
		{
			name:   "tokens fell but the reject rate did not",
			before: weeks([]int64{900_000, 910_000, 905_000, 895_000}, []float64{14, 15, 14, 15}),
			after:  weeks([]int64{300_000, 280_000, 310_000, 290_000}, []float64{15, 16, 15, 16}),
			want:   false,
		},
		{
			name:   "no drop at all",
			before: weeks([]int64{900_000, 910_000, 905_000, 895_000}, []float64{14, 15, 14, 15}),
			after:  weeks([]int64{910_000, 900_000, 905_000, 915_000}, []float64{14, 15, 14, 15}),
			want:   false,
		},
		{
			name:   "a rise is not a saving",
			before: weeks([]int64{300_000, 280_000, 310_000, 290_000}, []float64{8, 7, 8, 7}),
			after:  weeks([]int64{900_000, 910_000, 905_000, 895_000}, []float64{14, 15, 14, 15}),
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Judge(series(tt.before, tt.after), setting, MinWeeks)
			if v.Realized != tt.want {
				t.Errorf("realized = %v, want %v (%s)", v.Realized, tt.want, v.Reason)
			}
		})
	}
}

// A verdict needs MinWeeks of baseline and MinWeeks after the setting; before
// that it says how many weeks it still needs.
func TestJudgeWaitsForEnoughWeeks(t *testing.T) {
	full := []int64{900_000, 910_000, 905_000, 895_000}
	fullR := []float64{14, 15, 14, 15}
	after := []int64{300_000, 280_000, 310_000, 290_000}
	afterR := []float64{8, 7, 8, 7}
	tests := []struct {
		name          string
		before, after []Week
	}{
		{"too few weeks before", weeks(full[:2], fullR[:2]), weeks(after, afterR)},
		{"too few weeks after", weeks(full, fullR), weeks(after[:1], afterR[:1])},
		{"no weeks at all", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Judge(series(tt.before, tt.after), setting, MinWeeks)
			if v.Realized {
				t.Errorf("realized with %d before and %d after", len(tt.before), len(tt.after))
			}
			if v.Reason == "" {
				t.Errorf("no reason given while waiting")
			}
		})
	}
}

// The spread is the week-to-week movement of the baseline, not its range,
// so one unusual week does not hide a real drop.
func TestSpreadIsWeekToWeekMovement(t *testing.T) {
	before := weeks([]int64{100, 110, 90, 100}, []float64{10, 10, 10, 10})
	// The changes are 10, 20 and 10: a mean of 40/3, not the 90 range.
	if got, want := spreadOf(before, tokensOf), 40.0/3; math.Abs(got-want) > 1e-9 {
		t.Errorf("spread = %v, want the mean absolute week-to-week change %v", got, want)
	}
}
