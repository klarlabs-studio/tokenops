package effortwatch

import (
	"math"
	"strings"
	"testing"
	"time"
)

var monday = time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)

// series builds consecutive weeks: before at one level, after at another,
// each figure set by the given functions of the week's index.
func series(n int, level func(i int) string, cost func(i int) float64, firstTry func(i int) float64) []Week {
	out := make([]Week, n)
	for i := range n {
		out[i] = Week{
			Start: monday.AddDate(0, 0, 7*i), Instructions: 40, Dominant: level(i),
			USDPerInstruction: cost(i), FirstTryPct: firstTry(i), RejectedPct: 5, InterruptedPct: 4,
		}
	}
	return out
}

func switchAt(k int, from, to string) func(int) string {
	return func(i int) string {
		if i < k {
			return from
		}
		return to
	}
}

func TestLatestChange(t *testing.T) {
	tests := []struct {
		name  string
		weeks []Week
		ok    bool
		want  Change
	}{
		{name: "one level throughout", weeks: series(6, switchAt(99, "high", "high"), func(int) float64 { return 1 }, func(int) float64 { return 70 })},
		{name: "a change is found at the first week at the new level",
			weeks: series(8, switchAt(4, "xhigh", "medium"), func(int) float64 { return 1 }, func(int) float64 { return 70 }),
			ok:    true, want: Change{At: monday.AddDate(0, 0, 28), From: "xhigh", To: "medium"}},
		{name: "the latest of two changes",
			weeks: series(8, func(i int) string { return map[bool]string{true: "high", false: "low"}[i < 2 || i >= 6] }, func(int) float64 { return 1 }, func(int) float64 { return 70 }),
			ok:    true, want: Change{At: monday.AddDate(0, 0, 42), From: "low", To: "high"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := LatestChange(tt.weeks)
			if ok != tt.ok || (ok && got != tt.want) {
				t.Fatalf("got %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// A thin week, or one without a cost, neither marks a change nor counts.
func TestThinAndUncostedWeeksAreLeftOut(t *testing.T) {
	weeks := series(3, switchAt(99, "high", "high"), func(int) float64 { return 1 }, func(int) float64 { return 70 })
	weeks[1].Dominant, weeks[1].Instructions = "low", 3
	weeks = append(weeks, Week{Start: monday.AddDate(0, 0, 21), Instructions: 40, Dominant: "low"})
	if c, ok := LatestChange(weeks); ok {
		t.Fatalf("change %+v from a thin or uncosted week", c)
	}
}

func TestJudge(t *testing.T) {
	const margin = 5
	flat := func(int) float64 { return 70 }
	noisy := func(i int) float64 { return []float64{1.0, 1.4, 0.8, 1.2}[i%4] }
	tests := []struct {
		name          string
		weeks         []Week
		judged        bool
		realized      bool
		outcomesWorse bool
		reason        string
	}{
		{name: "too few weeks after the change",
			weeks:  series(6, switchAt(4, "xhigh", "medium"), func(int) float64 { return 1 }, flat),
			reason: "waiting for 2 more weeks since the change; have 2"},
		{name: "too few weeks before the change",
			weeks:  series(6, switchAt(2, "xhigh", "medium"), func(int) float64 { return 1 }, flat),
			reason: "waiting for 2 more weeks before the change; have 2"},
		{name: "cost fell beyond the movement and outcomes held",
			weeks: series(8, switchAt(4, "xhigh", "medium"), func(i int) float64 {
				if i < 4 {
					return 1.2 + 0.02*float64(i%2)
				}
				return 0.5
			}, flat),
			judged: true, realized: true},
		{name: "cost fell no further than the usual swing",
			weeks: series(8, switchAt(4, "xhigh", "medium"), func(i int) float64 {
				if i < 4 {
					return noisy(i)
				}
				return 0.85
			}, flat),
			judged: true, reason: "did not fall beyond"},
		{name: "first-try fell beyond the margin",
			weeks: series(8, switchAt(4, "xhigh", "medium"), func(i int) float64 {
				if i < 4 {
					return 1.2
				}
				return 0.5
			}, func(i int) float64 {
				if i < 4 {
					return 70
				}
				return 60
			}),
			judged: true, outcomesWorse: true, reason: "worsened"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, ok := LatestChange(tt.weeks)
			if !ok {
				t.Fatal("no change found")
			}
			v := Judge(tt.weeks, c, margin)
			if v.Judged != tt.judged || v.Realized != tt.realized || v.OutcomesWorse != tt.outcomesWorse {
				t.Fatalf("verdict = %+v", v)
			}
			if !strings.Contains(v.Reason, tt.reason) {
				t.Errorf("reason %q, want it to say %q", v.Reason, tt.reason)
			}
		})
	}
}

// The interval is the mean ± t·s/√n over the weeks either side.
func TestJudgeGivesIntervals(t *testing.T) {
	weeks := series(8, switchAt(4, "xhigh", "medium"), func(i int) float64 {
		return []float64{1.0, 1.2, 1.4, 1.2, 0.5, 0.5, 0.5, 0.5}[i]
	}, func(int) float64 { return 70 })
	c, _ := LatestChange(weeks)
	v := Judge(weeks, c, 5)
	// Before: mean 1.2, s = √(0.08/3), t(3) = 3.182.
	half := 3.182 * math.Sqrt(0.08/3) / 2
	if math.Abs(v.Cost.Before.Mean-1.2) > 1e-9 || math.Abs(v.Cost.Before.High-(1.2+half)) > 1e-9 || math.Abs(v.Cost.Before.Low-(1.2-half)) > 1e-9 {
		t.Errorf("before = %+v, want 1.2 ± %.4f", v.Cost.Before, half)
	}
	if v.Cost.After.Low != 0.5 || v.Cost.After.High != 0.5 {
		t.Errorf("after = %+v, want a point", v.Cost.After)
	}
	if v.WeeklyInstructionsAfter != 40 {
		t.Errorf("weekly instructions after = %v", v.WeeklyInstructionsAfter)
	}
}
