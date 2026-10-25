package routeguard

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
)

var pressureNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// window is a plan window used usedPct with resetsIn left until it resets,
// as the status line reports it.
func window(usedPct float64, resetsIn time.Duration) *claudelimits.Window {
	return &claudelimits.Window{UsedPct: usedPct, ResetsAt: pressureNow.Add(resetsIn).Unix()}
}

func reading(fiveHour, sevenDay *claudelimits.Window) claudelimits.Reading {
	return claudelimits.Reading{ObservedAt: pressureNow.Add(-2 * time.Minute), FiveHour: fiveHour, SevenDay: sevenDay}
}

// maintainerWeek is the week that prompted this: 54% left, 28 points ahead
// of an even spread, with the 5h window comfortable.
func maintainerWeek() *Pressure {
	p, _ := PressureFromLimits(reading(window(10, 4*time.Hour), window(46, 5*24*time.Hour+18*time.Hour)), pressureNow)
	return p
}

func TestPressureFromLimits(t *testing.T) {
	day := 24 * time.Hour
	for name, tc := range map[string]struct {
		r          claudelimits.Reading
		wantWindow string
	}{
		// 46% used with 18% of the week gone runs out days before the reset.
		"week ahead of pace": {reading(window(10, 4*time.Hour), window(46, 5*day+18*time.Hour)), "weekly"},
		// The same share on pace lasts: a share alone is not pressure.
		"week on pace": {reading(window(10, 4*time.Hour), window(46, 3*day+18*time.Hour)), ""},
		// Too early in the window to trust the projection, and well above
		// the floor.
		"projection too early": {reading(window(5, 4*time.Hour+50*time.Minute), window(3, 7*day-time.Hour)), ""},
		// Too early to project, but past the floor: pressure all the same.
		"floor early in the window": {reading(window(80, 4*time.Hour+40*time.Minute), nil), "5h"},
		// Runs out only hours before the reset, with a third left: not
		// worth a worse model.
		"runs out just before reset": {reading(nil, window(70, 58*time.Hour)), ""},
		// Past the floor yet lasting to the reset at this pace: no shortage.
		"low but lasting":      {reading(nil, window(80, 6*time.Hour)), ""},
		"tightest window wins": {reading(window(85, 2*time.Hour), window(80, 2*day)), "5h"},
		"used up":              {reading(window(100, time.Hour), nil), "5h"},
	} {
		t.Run(name, func(t *testing.T) {
			p, ok := PressureFromLimits(tc.r, pressureNow)
			if tc.wantWindow == "" {
				if ok {
					t.Fatalf("pressure %+v", p)
				}
				return
			}
			if !ok || p.Window != tc.wantWindow || p.Source != "claude_code_statusline" || p.ObservedAt.IsZero() {
				t.Fatalf("pressure %+v ok=%v, want window %q", p, ok, tc.wantWindow)
			}
		})
	}
}

// A reading TokenOps cannot trust says nothing, so routing stays as it was.
func TestPressureFailsOpen(t *testing.T) {
	stale := reading(nil, window(90, 2*24*time.Hour))
	stale.ObservedAt = pressureNow.Add(-31 * time.Minute)
	reset := reading(nil, &claudelimits.Window{UsedPct: 90, ResetsAt: pressureNow.Add(-time.Minute).Unix()})
	for name, r := range map[string]claudelimits.Reading{
		"empty": {}, "stale": stale, "window already reset": reset,
	} {
		if p, ok := PressureFromLimits(r, pressureNow); ok {
			t.Errorf("%s: pressure %+v", name, p)
		}
	}
	if p := ReadPressure(t.TempDir()+"/missing.json", boundaryledger.Dir(t.TempDir()), pressureNow); p != nil {
		t.Errorf("missing readings: pressure %+v", p)
	}
}

// ReadPressure prefers the status line's reading, which carries resets,
// and falls back to the daemon's figures only without a fresh one.
func TestReadPressure(t *testing.T) {
	dir := t.TempDir()
	limits := dir + "/claude-limits.json"
	figs := boundaryledger.Dir(dir + "/boundaries")
	if err := figs.PutFigures(boundaryledger.Figures{ObservedAt: pressureNow.Add(-time.Minute),
		PlanLeftPct: map[string]map[string]float64{"anthropic": {"week": 10}}}); err != nil {
		t.Fatal(err)
	}
	if p := ReadPressure(limits, figs, pressureNow); p == nil || p.Source != "daemon_glance" {
		t.Fatalf("figures fallback: %+v", p)
	}
	if err := claudelimits.Write(limits, reading(window(10, 4*time.Hour), window(46, 3*24*time.Hour+18*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if p := ReadPressure(limits, figs, pressureNow); p != nil {
		t.Errorf("a fresh status line reading on pace answers for itself: %+v", p)
	}
}

// Without a status line reading the daemon's figures carry the share left
// but no reset, so only the floor applies to them.
func TestPressureFromFigures(t *testing.T) {
	fig := boundaryledger.Figures{ObservedAt: pressureNow.Add(-time.Minute), PlanLeftPct: map[string]map[string]float64{
		"anthropic": {"5h": 70, "week": 20}, "openai": {"5h": 1},
	}}
	p, ok := PressureFromFigures(fig, pressureNow)
	if !ok || p.Window != "weekly" || p.LeftPct != 20 || p.Source != "daemon_glance" {
		t.Fatalf("pressure %+v ok=%v", p, ok)
	}
	fig.PlanLeftPct["anthropic"]["week"] = 40
	if p, ok := PressureFromFigures(fig, pressureNow); ok {
		t.Errorf("above the floor: %+v", p)
	}
	fig.PlanLeftPct["anthropic"]["week"] = 10
	fig.ObservedAt = pressureNow.Add(-6 * time.Minute)
	if p, ok := PressureFromFigures(fig, pressureNow); ok {
		t.Errorf("stale figures: %+v", p)
	}
}

func TestPressureWhy(t *testing.T) {
	if got := maintainerWeek().Why(); got != "Claude weekly runs out ~4d 6h before it resets at this pace (54% left)" {
		t.Errorf("why = %q", got)
	}
	p := &Pressure{Vendor: "Claude", Window: "5h", LeftPct: 12}
	if got := p.Why(); got != "Claude 5h has 12% left" {
		t.Errorf("why = %q", got)
	}
}

// Every kind on every tier, with and without pressure. Pressure moves
// edit and deep work one tier down, never lookups or research below what
// they need, never up.
func TestSubagentUnderPressure(t *testing.T) {
	const (
		lookup   = "find where the retention config is defined"
		research = "research how prefix keys work and summarise"
		edit     = "add a test for the parser"
		deep     = "debug why the router drops requests"
	)
	for _, tc := range []struct {
		name, prompt, requested, session string
		calm, pressed                    string // alias wanted; "" is no move
	}{
		{"lookup on opus", lookup, "opus", "", "haiku", "haiku"},
		{"lookup on haiku", lookup, "haiku", "", "", ""},
		{"research on opus", research, "opus", "", "sonnet", "sonnet"},
		{"research on sonnet", research, "sonnet", "", "", ""},
		{"edit on opus", edit, "opus", "", "", "sonnet"},
		{"edit on sonnet", edit, "sonnet", "", "", ""},
		{"edit on haiku", edit, "haiku", "", "", ""},
		{"edit on fable", edit, "", "claude-fable-5-1", "opus", "sonnet"},
		{"deep on fable", deep, "", "claude-fable-5-1", "", "opus"},
		{"deep on opus", deep, "opus", "", "", ""},
		{"deep on sonnet", deep, "sonnet", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := subagent(tc.prompt, tc.requested, tc.session)
			if d := EvaluateSubagent(in); d.ToAlias != tc.calm || d.Pressure != nil {
				t.Errorf("calm: moved to %q (pressure %v), want %q", d.ToAlias, d.Pressure, tc.calm)
			}
			in.Pressure = maintainerWeek()
			d := EvaluateSubagent(in)
			if d.ToAlias != tc.pressed {
				t.Fatalf("pressed: moved to %q, want %q (%+v)", d.ToAlias, tc.pressed, d)
			}
			// The pressure is credited only where it changed the move.
			if movedByPressure := tc.pressed != tc.calm; (d.Pressure != nil) != movedByPressure {
				t.Errorf("pressure credited = %v, want %v", d.Pressure != nil, movedByPressure)
			}
		})
	}
}

// The reason says why the move happened, in the operator's words.
func TestSubagentPressureReason(t *testing.T) {
	in := subagent("add a test for the parser", "opus", "")
	in.Pressure = maintainerWeek()
	d := EvaluateSubagent(in)
	if want := "Claude weekly runs out ~4d 6h before it resets at this pace (54% left); edit work moved from claude-opus-5 to claude-sonnet-5"; d.Reason != want {
		t.Errorf("reason = %q", d.Reason)
	}
}

// The model policy filters the candidates before the coach sees them: with
// the balanced tier ruled out, a pressed edit gets no further than its
// work alone would take it, and never onto a ruled-out model.
func TestSubagentPressureNeverPassesThePolicy(t *testing.T) {
	in := subagent("add a test for the parser", "", "claude-fable-5-1")
	in.Candidates = []string{"claude-haiku-4-5", "claude-opus-5", "claude-fable-5-1"}
	calm := EvaluateSubagent(in)
	in.Pressure = maintainerWeek()
	d := EvaluateSubagent(in)
	if d.To != calm.To || d.Pressure != nil || d.To == "claude-sonnet-5" {
		t.Errorf("pressed to %q (pressure %v), calm to %q", d.To, d.Pressure, calm.To)
	}
}
