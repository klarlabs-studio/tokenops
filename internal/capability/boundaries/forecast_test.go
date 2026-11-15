package boundaries

import (
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A pause records each plan window as the call read it: the share used,
// the pace, and when it runs out at that pace, so whether the pause kept
// the window from running out can be judged later.
func TestPauseRecordsTheWindowForecast(t *testing.T) {
	env := testEnv(t, false)
	if err := claudelimits.Write(env.LimitsPath, claudelimits.Reading{
		ObservedAt: testNow.Add(-time.Minute),
		FiveHour:   &claudelimits.Window{UsedPct: 75, ResetsAt: testNow.Add(150 * time.Minute).Unix()},
	}); err != nil {
		t.Fatal(err)
	}
	if res, _ := Guard(paceRule(t), call("s1"), env); res.Verdict != VerdictDeny {
		t.Fatalf("not paused: %+v", res)
	}
	envs, err := env.Dir.Read()
	if err != nil || len(envs) == 0 {
		t.Fatalf("no decision recorded: %v", err)
	}
	d := envs[len(envs)-1].Payload.(*eventschema.DecisionEvent)
	if len(d.Forecasts) != 1 {
		t.Fatalf("forecasts = %+v; want the 5h window", d.Forecasts)
	}
	f := d.Forecasts[0]
	// 75% used 150 minutes into 300: 30 points an hour, out in 50 minutes.
	if f.Window != "5h" || f.UsedPct != 75 || math.Abs(f.PctPerHour-30) > 0.01 ||
		!f.RunsOutAt.Equal(testNow.Add(50*time.Minute).UTC()) || !f.ResetsAt.Equal(testNow.Add(150*time.Minute).UTC().Truncate(time.Second)) {
		t.Errorf("forecast = %+v", f)
	}
}

func TestWindowForecast(t *testing.T) {
	now := testNow
	tests := []struct {
		name     string
		w        plans.VendorWindow
		ok       bool
		runsOut  time.Duration // 0 when it lasts
		perHour  float64
		usedUpAt bool
	}{
		{"runs out at the average so far", plans.VendorWindow{Name: "5h", UsedPct: 75, Duration: 5 * time.Hour, ResetsAt: now.Add(150 * time.Minute)},
			true, 50 * time.Minute, 30, false},
		{"lasts to its reset", plans.VendorWindow{Name: "5h", UsedPct: 20, Duration: 5 * time.Hour, ResetsAt: now.Add(150 * time.Minute)},
			true, 0, 8, false},
		{"already used up", plans.VendorWindow{Name: "5h", UsedPct: 100, Duration: 5 * time.Hour, ResetsAt: now.Add(time.Hour)},
			true, 0, 0, true},
		{"no reset ahead", plans.VendorWindow{Name: "week", UsedPct: 50}, false, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ok := windowForecast("anthropic", windowName(tt.w), tt.w, now)
			if ok != tt.ok {
				t.Fatalf("ok = %v", ok)
			}
			if !ok {
				return
			}
			switch {
			case tt.usedUpAt:
				if !f.RunsOutAt.Equal(now.UTC()) {
					t.Errorf("used up: runs out at %s; want now", f.RunsOutAt)
				}
			case tt.runsOut == 0:
				if !f.RunsOutAt.IsZero() {
					t.Errorf("lasting window runs out at %s", f.RunsOutAt)
				}
			default:
				if !f.RunsOutAt.Equal(now.Add(tt.runsOut).UTC()) {
					t.Errorf("runs out at %s; want %s", f.RunsOutAt, now.Add(tt.runsOut))
				}
			}
			if math.Abs(f.PctPerHour-tt.perHour) > 0.01 {
				t.Errorf("pace = %v; want %v", f.PctPerHour, tt.perHour)
			}
		})
	}
}

// The daemon's figures carry the forecast for the pause point to record.
func TestFiguresCarryTheForecast(t *testing.T) {
	reports := []plans.HeadroomReport{{Provider: "anthropic", Windows: []plans.VendorWindow{
		{Name: "5h", UsedPct: 75, Duration: 5 * time.Hour, ResetsAt: testNow.Add(150 * time.Minute)},
	}}}
	f := Figures(reports, nil, testNow)
	if got, ok := f.PlanForecast["anthropic"]["5h"]; !ok || got.UsedPct != 75 || got.RunsOutAt.IsZero() {
		t.Errorf("figures forecast = %+v, %v", got, ok)
	}
}
