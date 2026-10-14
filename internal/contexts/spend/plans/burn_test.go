package plans

import (
	"testing"
	"time"
)

func TestAssessBurn(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	fiveHour := func(used float64, resetsIn time.Duration) QuotaWindow {
		return QuotaWindow{Label: "5-hour", UsedPct: used, ResetsAt: now.Add(resetsIn), Duration: 5 * time.Hour}
	}
	at := func(ago time.Duration, ws ...QuotaWindow) *QuotaReading {
		return &QuotaReading{At: now.Add(-ago), Windows: ws}
	}
	tests := []struct {
		name        string
		cur         QuotaReading
		long, short *QuotaReading
		warn        bool
		runsOutIn   time.Duration
		stale       bool
	}{
		{
			// 30 points in the last hour and 8 in the last 15 minutes:
			// both run the remaining 40 out before the 2h reset.
			name:  "both look-backs run out",
			cur:   *at(0, fiveHour(60, 2*time.Hour)),
			long:  at(time.Hour, fiveHour(30, 2*time.Hour)),
			short: at(15*time.Minute, fiveHour(52, 2*time.Hour)),
			warn:  true, runsOutIn: 80 * time.Minute, // the later of 80m (long) and 75m (short)
		},
		{
			// The hour was heavy but the last 15 minutes were idle: the
			// burst is over, so stay quiet.
			name:  "short look-back idle",
			cur:   *at(0, fiveHour(60, 2*time.Hour)),
			long:  at(time.Hour, fiveHour(30, 2*time.Hour)),
			short: at(15*time.Minute, fiveHour(60, 2*time.Hour)),
		},
		{
			// A 15-minute spike in an otherwise slow hour: the long
			// look-back lasts, so stay quiet.
			name:  "long look-back lasts",
			cur:   *at(0, fiveHour(60, 4*time.Hour)),
			long:  at(time.Hour, fiveHour(52, 4*time.Hour)),
			short: at(15*time.Minute, fiveHour(52, 4*time.Hour)),
		},
		{
			// 90% at minute 290 of 300 is fine: 10 points an hour lasts
			// the ten minutes left.
			name:  "late and high but lasting",
			cur:   *at(0, fiveHour(90, 10*time.Minute)),
			long:  at(time.Hour, fiveHour(80, 10*time.Minute)),
			short: at(15*time.Minute, fiveHour(88, 10*time.Minute)),
		},
		{
			// 50% at minute 30 is not: the window is younger than the
			// long look-back, so its start (0%) stands in for it.
			name:  "early and fast",
			cur:   *at(0, fiveHour(50, 4*time.Hour+30*time.Minute)),
			long:  at(time.Hour, fiveHour(70, 30*time.Minute)), // the previous window
			short: at(15*time.Minute, fiveHour(25, 4*time.Hour+30*time.Minute)),
			warn:  true, runsOutIn: 30 * time.Minute,
		},
		{
			name:  "stale reading",
			cur:   *at(20*time.Minute, fiveHour(60, 2*time.Hour)),
			long:  at(80*time.Minute, fiveHour(30, 2*time.Hour)),
			short: at(35*time.Minute, fiveHour(52, 2*time.Hour)),
			stale: true,
		},
		{
			// No reading an hour back and the window is older than that:
			// one look-back is not enough to warn on.
			name:  "no long history",
			cur:   *at(0, fiveHour(60, 2*time.Hour)),
			short: at(15*time.Minute, fiveHour(52, 2*time.Hour)),
		},
		{
			name:  "used up is not a forecast",
			cur:   *at(0, fiveHour(100, 2*time.Hour)),
			long:  at(time.Hour, fiveHour(60, 2*time.Hour)),
			short: at(15*time.Minute, fiveHour(90, 2*time.Hour)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := AssessBurn(tt.cur, tt.long, tt.short, now)
			if v.Stale != tt.stale {
				t.Fatalf("stale = %v, want %v", v.Stale, tt.stale)
			}
			if got := len(v.Warnings) > 0; got != tt.warn {
				t.Fatalf("warn = %v, want %v (%+v)", got, tt.warn, v.Warnings)
			}
			if !tt.warn {
				return
			}
			w := v.Warnings[0]
			if got := w.RunsOutAt.Sub(tt.cur.At); got != tt.runsOutIn {
				t.Errorf("runs out in %v, want %v", got, tt.runsOutIn)
			}
			if w.LongBurn <= 1 || w.ShortBurn <= 1 {
				t.Errorf("burn rates %v / %v should exceed the even rate", w.LongBurn, w.ShortBurn)
			}
			if w.BeforeReset() <= 0 {
				t.Errorf("before reset = %v", w.BeforeReset())
			}
		})
	}
}

// The window that runs out first comes first.
func TestAssessBurnOrdersByRunOut(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	five := QuotaWindow{Label: "5-hour", UsedPct: 60, ResetsAt: now.Add(2 * time.Hour), Duration: 5 * time.Hour}
	week := QuotaWindow{Label: "weekly", UsedPct: 95, ResetsAt: now.Add(48 * time.Hour), Duration: 7 * 24 * time.Hour}
	pastFive, pastWeek := five, week
	pastFive.UsedPct, pastWeek.UsedPct = 30, 90
	shortFive, shortWeek := five, week
	shortFive.UsedPct, shortWeek.UsedPct = 52, 94
	v := AssessBurn(QuotaReading{At: now, Windows: []QuotaWindow{five, week}},
		&QuotaReading{At: now.Add(-time.Hour), Windows: []QuotaWindow{pastFive, pastWeek}},
		&QuotaReading{At: now.Add(-15 * time.Minute), Windows: []QuotaWindow{shortFive, shortWeek}}, now)
	if len(v.Warnings) != 2 || v.Warnings[0].Window.Label != "weekly" {
		t.Fatalf("warnings = %+v", v.Warnings)
	}
}
