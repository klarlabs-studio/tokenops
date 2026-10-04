package plans

import (
	"testing"
	"time"
)

func TestPaceAt(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	// 26h into a week is 15.5% gone.
	in := func(left time.Duration) time.Time { return now.Add(left) }
	tests := []struct {
		name string
		w    VendorWindow
		want *WindowPace
	}{
		{"ahead and runs out", VendorWindow{UsedPct: 52, Duration: week, ResetsAt: in(week - 26*time.Hour)},
			&WindowPace{Status: PaceAhead, DeltaPct: 37, RunsOutIn: 24 * time.Hour}},
		{"behind lasts", VendorWindow{UsedPct: 5, Duration: week, ResetsAt: in(week - 26*time.Hour)},
			&WindowPace{Status: PaceBehind, DeltaPct: -10, LastsToReset: true}},
		{"on pace", VendorWindow{UsedPct: 47, Duration: 10 * time.Hour, ResetsAt: in(5*time.Hour + 6*time.Minute)},
			&WindowPace{Status: PaceOnPace, DeltaPct: -2, LastsToReset: true}},
		{"nothing used", VendorWindow{UsedPct: 0, Duration: week, ResetsAt: in(time.Hour)}, nil},
		{"no length", VendorWindow{UsedPct: 30, ResetsAt: in(time.Hour)}, nil},
		{"no reset", VendorWindow{UsedPct: 30, Duration: week}, nil},
		{"reset past", VendorWindow{UsedPct: 30, Duration: week, ResetsAt: in(-time.Minute)}, nil},
	}
	for _, tt := range tests {
		got := tt.w.PaceAt(now)
		switch {
		case (got == nil) != (tt.want == nil):
			t.Errorf("%s: pace = %+v, want %+v", tt.name, got, tt.want)
		case got != nil && *got != *tt.want:
			t.Errorf("%s: pace = %+v, want %+v", tt.name, *got, *tt.want)
		}
	}
}
