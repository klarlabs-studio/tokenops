package forecast

import (
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// A Wednesday at noon, UTC.
var noon = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

// daysAgo is the start of the UTC day n days before noon's.
func daysAgo(n int) time.Time {
	return noon.Truncate(24*time.Hour).AddDate(0, 0, -n)
}

// steady is a row of cost on each of the n days before noon's.
func steady(cost float64, n int) []analytics.Row {
	rows := make([]analytics.Row, 0, n)
	for i := n; i >= 1; i-- {
		rows = append(rows, analytics.Row{BucketStart: daysAgo(i), CostUSD: cost, TotalTokens: int64(cost * 1000)})
	}
	return rows
}

func values(ps []Point) []float64 {
	out := make([]float64, len(ps))
	for i, p := range ps {
		out[i] = p.Value
	}
	return out
}

func sum(ps []Prediction) float64 {
	var s float64
	for _, p := range ps {
		s += p.Value
	}
	return s
}

func total(ps []Point) float64 {
	var s float64
	for _, p := range ps {
		s += p.Value
	}
	return s
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDailyFrom(t *testing.T) {
	tests := map[string]struct {
		rows  []analytics.Row
		since time.Time
		want  []float64
		first time.Time
	}{
		"a day with no row is a day at zero": {
			rows: []analytics.Row{
				{BucketStart: daysAgo(4), CostUSD: 20},
				{BucketStart: daysAgo(2), CostUSD: 10},
			},
			want: []float64{20, 0, 10, 0}, first: daysAgo(4),
		},
		"the running day is left out": {
			rows: []analytics.Row{
				{BucketStart: daysAgo(2), CostUSD: 20},
				{BucketStart: daysAgo(1), CostUSD: 20},
				{BucketStart: daysAgo(0), CostUSD: 1},
			},
			want: []float64{20, 20}, first: daysAgo(2),
		},
		"the day the read began in is left out: it was read in part": {
			rows: []analytics.Row{
				{BucketStart: daysAgo(3), CostUSD: 4},
				{BucketStart: daysAgo(2), CostUSD: 20},
				{BucketStart: daysAgo(1), CostUSD: 20},
			},
			since: daysAgo(3).Add(12 * time.Hour),
			want:  []float64{20, 20}, first: daysAgo(2),
		},
		"a read that began at midnight keeps its first day": {
			rows: []analytics.Row{
				{BucketStart: daysAgo(2), CostUSD: 4},
				{BucketStart: daysAgo(1), CostUSD: 20},
			},
			since: daysAgo(2),
			want:  []float64{4, 20}, first: daysAgo(2),
		},
		"the series starts with the first day that has a row": {
			rows:  []analytics.Row{{BucketStart: daysAgo(2), CostUSD: 20}},
			since: daysAgo(30),
			want:  []float64{20, 0}, first: daysAgo(2),
		},
		"rows of one day are one point": {
			rows: []analytics.Row{
				{BucketStart: daysAgo(1), CostUSD: 3},
				{BucketStart: daysAgo(1).Add(5 * time.Hour), CostUSD: 4},
			},
			want: []float64{7}, first: daysAgo(1),
		},
		"rows out of order": {
			rows: []analytics.Row{
				{BucketStart: daysAgo(1), CostUSD: 2},
				{BucketStart: daysAgo(3), CostUSD: 1},
			},
			want: []float64{1, 0, 2}, first: daysAgo(3),
		},
		"nothing before today": {
			rows: []analytics.Row{{BucketStart: daysAgo(0), CostUSD: 9}},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			d := DailyFrom(tc.rows, CostUSD, tc.since, noon)
			got := values(d.Points)
			if len(got) != len(tc.want) {
				t.Fatalf("points = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("points = %v, want %v", got, tc.want)
				}
			}
			if len(got) > 0 && !d.Points[0].At.Equal(tc.first) {
				t.Errorf("first day = %v, want %v", d.Points[0].At, tc.first)
			}
			for i := 1; i < len(d.Points); i++ {
				if step := d.Points[i].At.Sub(d.Points[i-1].At); step != 24*time.Hour {
					t.Errorf("point %d is %v after the one before it, want a day", i, step)
				}
			}
		})
	}
	if d := DailyFrom(steady(1, 3), nil, time.Time{}, noon); len(d.Points) != 0 {
		t.Errorf("no accessor gave %v", d.Points)
	}
}

// A morning's dollar read as a whole day pulled a steady $20 a day down
// to a forecast of $5, then $2, then nothing.
func TestAheadIsNotPulledDownByTheRunningDay(t *testing.T) {
	rows := append(steady(20, 29), analytics.Row{BucketStart: daysAgo(0), CostUSD: 1})
	got := DailyFrom(rows, CostUSD, noon.AddDate(0, 0, -30), noon).Ahead(3)
	if len(got) != 3 {
		t.Fatalf("got %d predictions, want 3", len(got))
	}
	for i, p := range got {
		if !near(p.Value, 20) {
			t.Errorf("day %d forecast %v, want 20", i+1, p.Value)
		}
		if want := daysAgo(-(i + 1)); !p.At.Equal(want) {
			t.Errorf("day %d is at %v, want %v: the days after today", i+1, p.At, want)
		}
	}
}

// Spend on every other day is half the spend of every day. With the empty
// days left out the series read as $20 on consecutive days.
func TestAheadCountsTheDaysNothingWasSpent(t *testing.T) {
	var rows []analytics.Row
	for i := 28; i >= 1; i -= 2 {
		rows = append(rows, analytics.Row{BucketStart: daysAgo(i), CostUSD: 20})
	}
	d := DailyFrom(rows, CostUSD, time.Time{}, noon)
	if len(d.Points) != 28 {
		t.Fatalf("%d points, want the 28 days from the first row to yesterday", len(d.Points))
	}
	if week := sum(d.Ahead(7)); !near(week, 70) {
		t.Errorf("a week forecast at $%.2f, want $70: $20 on every other day", week)
	}
}

// workWeek is $70 on each working day and nothing at the weekend, on the
// n days before now's.
func workWeek(now time.Time, n int) []analytics.Row {
	today := now.UTC().Truncate(24 * time.Hour)
	var rows []analytics.Row
	for i := n; i >= 1; i-- {
		at := today.AddDate(0, 0, -i)
		if wd := at.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		rows = append(rows, analytics.Row{BucketStart: at, CostUSD: 70})
	}
	return rows
}

// A week of work is $350 on whatever day it is asked. A trend through the
// last few days read the weekend as a fall to nothing and Monday as a
// steep climb, and forecast a week at anything from $0 to $700.
func TestAheadFollowsTheWeek(t *testing.T) {
	for off := range 7 {
		now := noon.AddDate(0, 0, off)
		t.Run(now.Weekday().String(), func(t *testing.T) {
			d := DailyFrom(workWeek(now, 29), CostUSD, now.AddDate(0, 0, -30), now)
			week := d.Ahead(7)
			if !near(sum(week), 350) {
				t.Errorf("a week forecast at $%.2f, want $350", sum(week))
			}
			for _, p := range week {
				want := 70.0
				if wd := p.At.Weekday(); wd == time.Saturday || wd == time.Sunday {
					want = 0
				}
				if !near(p.Value, want) {
					t.Errorf("%s forecast at $%.2f, want $%.0f", p.At.Weekday(), p.Value, want)
				}
			}
			// What is left of the week, to the Monday it ends on.
			monday := now.Truncate(24*time.Hour).AddDate(0, 0, 7-(int(now.Weekday())+6)%7)
			left := 0.0
			for at := now.Truncate(24 * time.Hour); at.Before(monday); at = at.AddDate(0, 0, 1) {
				if wd := at.Weekday(); wd != time.Saturday && wd != time.Sunday {
					left += 70
					if at.Before(now) {
						left -= 35 // half of today is over
					}
				}
			}
			if got := sum(d.Rest(monday)); !near(got, left) {
				t.Errorf("the rest of the week forecast at $%.2f, want $%.2f", got, left)
			}
		})
	}
}

// Short of two of each weekday there is no week to follow: a day ahead is
// the mean day.
func TestAheadBeforeTwoWeeksIsTheMeanDay(t *testing.T) {
	tests := map[string]struct {
		days int
		flat bool
	}{
		"thirteen days": {days: 13, flat: true},
		"fourteen days": {days: 14, flat: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			d := DailyFrom(workWeek(noon, tc.days), CostUSD, daysAgo(tc.days), noon)
			if len(d.Points) != tc.days {
				t.Fatalf("%d points, want %d", len(d.Points), tc.days)
			}
			week := d.Ahead(7)
			flat := true
			for _, p := range week {
				flat = flat && near(p.Value, week[0].Value)
			}
			if flat != tc.flat {
				t.Errorf("flat = %v, want %v: %+v", flat, tc.flat, week)
			}
			if tc.flat {
				if want := total(d.Points) / float64(tc.days); !near(week[0].Value, want) {
					t.Errorf("a day forecast at $%.2f, want the mean day $%.2f", week[0].Value, want)
				}
			}
		})
	}
}

// The band is the spread of the days about what was expected of them: none
// when every day was as expected, and never below zero.
func TestAheadBand(t *testing.T) {
	steadyDay := DailyFrom(steady(20, 9), CostUSD, time.Time{}, noon).Ahead(1)[0]
	if steadyDay.Lower != 20 || steadyDay.Upper != 20 {
		t.Errorf("a steady series has band %v..%v, want none", steadyDay.Lower, steadyDay.Upper)
	}
	// 4 and 8 on alternate days: mean 6, and each day 2 from it.
	rows := steady(4, 10)
	for i := range rows {
		if i%2 == 0 {
			rows[i].CostUSD = 8
		}
	}
	got := DailyFrom(rows, CostUSD, time.Time{}, noon).Ahead(2)
	sigma := math.Sqrt(10 * 4.0 / 9)
	for _, p := range got {
		if !near(p.Value, 6) || !near(p.Upper, 6+ConfidenceZ95*sigma) || !near(p.Lower, 6-ConfidenceZ95*sigma) {
			t.Errorf("got %+v, want 6 with a band of %.3f either side", p, ConfidenceZ95*sigma)
		}
	}
	// A work week is as expected on every day once each weekday has its own
	// mean, so it has no band; read as one mean day it has a wide one.
	if p := DailyFrom(workWeek(noon, 28), CostUSD, time.Time{}, noon).Ahead(1)[0]; p.Lower != p.Value || p.Upper != p.Value {
		t.Errorf("a work week read by weekday has band %v..%v about %v", p.Lower, p.Upper, p.Value)
	}
	if p := DailyFrom(workWeek(noon, 13), CostUSD, time.Time{}, noon).Ahead(1)[0]; p.Lower != 0 || p.Upper <= p.Value {
		t.Errorf("a work week read as one mean day has band %v..%v about %v, want a wide one cut at zero", p.Lower, p.Upper, p.Value)
	}
}

func TestZero(t *testing.T) {
	if !DailyFrom(steady(0, 5), CostUSD, time.Time{}, noon).Zero() {
		t.Error("five days at nothing are not zero")
	}
	rows := steady(0, 5)
	rows[2].CostUSD = 0.01
	if DailyFrom(rows, CostUSD, time.Time{}, noon).Zero() {
		t.Error("a day that cost a cent is zero")
	}
}

// At midnight today has not begun, so it is the first day ahead.
func TestAheadStartsWithADayThatHasNotBegun(t *testing.T) {
	midnight := daysAgo(0)
	got := DailyFrom(steady(20, 9), CostUSD, time.Time{}, midnight).Ahead(2)
	if len(got) != 2 || !got[0].At.Equal(midnight) || !got[1].At.Equal(daysAgo(-1)) {
		t.Fatalf("got %+v, want today and tomorrow", got)
	}
	got = DailyFrom(steady(20, 9), CostUSD, time.Time{}, midnight.Add(time.Second)).Ahead(2)
	if len(got) != 2 || !got[0].At.Equal(daysAgo(-1)) {
		t.Fatalf("got %+v, want tomorrow and the day after", got)
	}
}

func TestAheadNeedsHistoryAndAHorizon(t *testing.T) {
	if got := DailyFrom(steady(5, 1), CostUSD, time.Time{}, noon).Ahead(3); got != nil {
		t.Errorf("one day of history forecast %v", got)
	}
	if got := DailyFrom(steady(5, 9), CostUSD, time.Time{}, noon).Ahead(0); got != nil {
		t.Errorf("no horizon forecast %v", got)
	}
}

func TestRest(t *testing.T) {
	midnight := daysAgo(-1)
	monday := daysAgo(-5) // the Monday after noon's Wednesday
	tests := map[string]struct {
		now, end time.Time
		want     []float64
		firstAt  time.Time
	}{
		"the rest of today is the share of the day that is left": {
			now: noon, end: midnight, want: []float64{3}, firstAt: noon,
		},
		"late in the day little is left": {
			now: noon.Add(9 * time.Hour), end: midnight, want: []float64{0.75}, firstAt: noon.Add(9 * time.Hour),
		},
		"a week counts today's rest and the whole days up to its end": {
			now: noon, end: monday, want: []float64{3, 6, 6, 6, 6}, firstAt: noon,
		},
		"a period that ends within a day counts that day in part": {
			now: noon, end: midnight.Add(6 * time.Hour), want: []float64{3, 1.5}, firstAt: noon,
		},
		"at midnight today is a whole day": {
			now: daysAgo(0), end: midnight, want: []float64{6}, firstAt: daysAgo(0),
		},
		"a period that is over": {now: noon, end: noon},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// The same 13 whole days of history whatever the hour.
			d := DailyFrom(steady(6, 13), CostUSD, time.Time{}, tc.now)
			got := d.Rest(tc.end)
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want values %v", got, tc.want)
			}
			for i, p := range got {
				if !near(p.Value, tc.want[i]) {
					t.Errorf("part %d = %v, want %v", i, p.Value, tc.want[i])
				}
				if p.At.Before(tc.now) || !p.At.Before(tc.end) {
					t.Errorf("part %d is at %v, outside what is left (%v to %v)", i, p.At, tc.now, tc.end)
				}
				if p.Lower > p.Value || p.Upper < p.Value {
					t.Errorf("part %d: band %v..%v does not hold %v", i, p.Lower, p.Upper, p.Value)
				}
			}
			if len(got) > 0 && !got[0].At.Equal(tc.firstAt) {
				t.Errorf("first part at %v, want %v", got[0].At, tc.firstAt)
			}
		})
	}
}

// The band is cut to the same share as the value.
func TestRestScalesTheBand(t *testing.T) {
	rows := steady(6, 13)
	for i := range rows {
		if i%2 == 0 {
			rows[i].CostUSD = 10
		}
	}
	d := DailyFrom(rows, CostUSD, time.Time{}, noon)
	full := d.Ahead(1)[0] // a whole day: before two weeks every day is the mean day
	half := d.Rest(daysAgo(-1))[0]
	if full.Upper <= full.Value || full.Lower <= 0 {
		t.Fatalf("fixture has no band either side: %+v", full)
	}
	if !near(half.Value, full.Value/2) || !near(half.Upper, full.Upper/2) || !near(half.Lower, full.Lower/2) {
		t.Errorf("half a day = %+v, want half of %+v", half, full)
	}
}
