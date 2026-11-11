package budgets

import (
	"context"
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/budget"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// dailySource has spent `spent` in the window it is asked about, and
// perDay dollars and perDay thousand tokens on each of the 29 days before
// now's. Its daily rows are those of a read that began 30 days before now:
// the first and the last are days read in part.
type dailySource struct {
	now    time.Time
	spent  float64
	perDay float64
	asked  []analytics.Filter
}

func (s *dailySource) Summarize(context.Context, analytics.Filter) (analytics.Summary, error) {
	return analytics.Summary{CostUSD: s.spent, TotalTokens: int64(s.spent * 1000)}, nil
}

func (s *dailySource) AggregateBy(_ context.Context, f analytics.Filter, _ analytics.Bucket, _ analytics.Group) ([]analytics.Row, error) {
	s.asked = append(s.asked, f)
	today := s.now.UTC().Truncate(24 * time.Hour)
	// The day the read began in, as far as the read reaches into it.
	rows := []analytics.Row{{BucketStart: today.AddDate(0, 0, -30), CostUSD: s.perDay / 2, TotalTokens: int64(s.perDay * 500)}}
	for i := 29; i >= 1; i-- {
		rows = append(rows, analytics.Row{BucketStart: today.AddDate(0, 0, -i), CostUSD: s.perDay, TotalTokens: int64(s.perDay * 1000)})
	}
	// Today so far, as the store returns it: part of the window's spend,
	// not a day of history.
	return append(rows, analytics.Row{BucketStart: today, CostUSD: s.spent, TotalTokens: int64(s.spent * 1000)}), nil
}

// A breach forecast is what the window has spent plus what is left of it:
// the rest of today, not a whole day on top of today's spend, and no day
// past the window's end.
func TestForecastBreachCountsOnlyWhatIsLeftOfTheWindow(t *testing.T) {
	wednesday := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	tests := map[string]struct {
		limit     budget.Limit
		now       time.Time
		spent     float64
		perDay    float64
		projected float64 // 0: no breach forecast
		breachAt  time.Time
	}{
		"a steady $6 a day stays inside $10 with $5 spent at noon": {
			limit: budget.Limit{Name: "d", Window: budget.WindowDaily, LimitUSD: 10},
			now:   wednesday, spent: 5, perDay: 6,
		},
		"$8 spent at noon and half a $6 day to come breaks $10": {
			limit: budget.Limit{Name: "d", Window: budget.WindowDaily, LimitUSD: 10},
			now:   wednesday, spent: 8, perDay: 6, projected: 11, breachAt: wednesday,
		},
		"late in the day little is left to come": {
			limit: budget.Limit{Name: "d", Window: budget.WindowDaily, LimitUSD: 10},
			now:   wednesday.Add(9 * time.Hour), spent: 9, perDay: 6,
		},
		"a week counts the rest of today and the days to Monday": {
			limit: budget.Limit{Name: "w", Window: budget.WindowWeekly, LimitUSD: 100},
			now:   wednesday, spent: 50, perDay: 10, // 50 + 5 + 4×10 = 95
		},
		"a week that will break its limit breaks it on the day it does": {
			limit: budget.Limit{Name: "w", Window: budget.WindowWeekly, LimitUSD: 100},
			now:   wednesday, spent: 56, perDay: 10, projected: 101,
			breachAt: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), // Sunday
		},
		"a month counts the days to its end": {
			limit: budget.Limit{Name: "m", Window: budget.WindowMonthly, LimitUSD: 300},
			now:   wednesday, spent: 50, perDay: 10, // 50 + 5 + 24×10 = 295
		},
		"a month that will break its limit": {
			limit: budget.Limit{Name: "m", Window: budget.WindowMonthly, LimitUSD: 300},
			now:   wednesday, spent: 56, perDay: 10, projected: 301,
			breachAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC),
		},
		"a token budget is forecast in tokens": {
			limit: budget.Limit{Name: "t", Window: budget.WindowDaily, LimitTokens: 10_000, Basis: budget.BasisTokens},
			now:   wednesday, spent: 8, perDay: 6, projected: 11_000, breachAt: wednesday,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			src := &dailySource{now: tc.now, spent: tc.spent, perDay: tc.perDay}
			w := &Watcher{Source: src, Limits: []budget.Limit{tc.limit}}
			var got *Alert
			for _, a := range w.Tick(context.Background(), tc.now).Alerts {
				if a.Kind == budget.AlertForecastBreach {
					got = &a
				}
			}
			if tc.projected == 0 {
				if got != nil {
					t.Fatalf("forecast a breach at %v, projected %v", got.BreachAt, got.ProjectedUSD)
				}
				return
			}
			if got == nil {
				t.Fatal("no breach forecast")
			}
			if math.Abs(got.ProjectedUSD-tc.projected) > 1e-6 {
				t.Errorf("projected %v, want %v", got.ProjectedUSD, tc.projected)
			}
			if !got.BreachAt.Equal(tc.breachAt) {
				t.Errorf("breach at %v, want %v", got.BreachAt, tc.breachAt)
			}
		})
	}
}

// The forecast reads enough weeks for each weekday to have its own days.
func TestForecastReadsThirtyDays(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	src := &dailySource{now: now, spent: 1, perDay: 1}
	w := &Watcher{Source: src, Limits: []budget.Limit{{Name: "d", Window: budget.WindowDaily, LimitUSD: 10}}}
	w.Tick(context.Background(), now)
	if len(src.asked) != 1 || !src.asked[0].Since.Equal(now.AddDate(0, 0, -30)) {
		t.Errorf("history asked %+v, want the 30 days before now", src.asked)
	}
}

// A weekend on which nothing is ever spent does not forecast a weekday's
// spend: a daily budget nearly used on a Saturday is not about to break.
func TestForecastBreachFollowsTheWeek(t *testing.T) {
	saturday := time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)
	src := &weekdaySource{now: saturday, spent: 9, perDay: 8}
	w := &Watcher{Source: src, Limits: []budget.Limit{{Name: "d", Window: budget.WindowDaily, LimitUSD: 10, WarnAt: 0.95, CritAt: 0.99}}}
	if alerts := w.Tick(context.Background(), saturday).Alerts; len(alerts) != 0 {
		t.Errorf("a Saturday forecast a breach from the weekdays' spend: %+v", alerts)
	}
	monday := saturday.AddDate(0, 0, 2)
	src = &weekdaySource{now: monday, spent: 5, perDay: 8}
	w = &Watcher{Source: src, Limits: []budget.Limit{{Name: "d", Window: budget.WindowDaily, LimitUSD: 10}}}
	alerts := w.Tick(context.Background(), monday).Alerts
	if len(alerts) != 1 || alerts[0].Kind != budget.AlertForecastBreach || math.Abs(alerts[0].ProjectedUSD-11) > 1e-6 {
		t.Errorf("a Monday at $5 by 06:00 with $8 working days = %+v, want a breach forecast at $11", alerts)
	}
}

// weekdaySource is a dailySource that spent nothing at weekends.
type weekdaySource dailySource

func (s *weekdaySource) Summarize(ctx context.Context, f analytics.Filter) (analytics.Summary, error) {
	return (*dailySource)(s).Summarize(ctx, f)
}

func (s *weekdaySource) AggregateBy(ctx context.Context, f analytics.Filter, b analytics.Bucket, g analytics.Group) ([]analytics.Row, error) {
	rows, err := (*dailySource)(s).AggregateBy(ctx, f, b, g)
	var out []analytics.Row
	for _, r := range rows {
		if wd := r.BucketStart.Weekday(); wd != time.Saturday && wd != time.Sunday {
			out = append(out, r)
		}
	}
	return out, err
}
