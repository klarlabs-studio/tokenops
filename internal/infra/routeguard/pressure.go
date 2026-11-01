package routeguard

import (
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
)

// Pressure is the Claude plan running short (plans.Pressure), as the hooks
// read it.
type Pressure = plans.Pressure

const (
	// pressureLimitsFreshFor matches the boundaries' reading of the same
	// file: Claude Code rewrites it every turn, so an older one belongs to
	// a session that stopped.
	pressureLimitsFreshFor = 30 * time.Minute
	// pressureFiguresFreshFor matches the boundaries' reading of the
	// daemon's figures, which it writes every minute.
	pressureFiguresFreshFor = 5 * time.Minute
)

// ReadPressure reads the Claude plan's pressure from what is already on the
// machine: the status line's reading first (it carries resets, so pace),
// the daemon's figures second (share left only). Both are local files; it
// never calls the daemon, so a hook stays fast, and any failure reads as
// no pressure. An empty limitsPath is the status line's default file.
func ReadPressure(limitsPath string, figures boundaryledger.Dir, now time.Time) *Pressure {
	p, _ := ReadPressureKnown(limitsPath, figures, now)
	return p
}

// ReadPressureKnown is ReadPressure that also says whether a fresh reading
// answered: known with a nil pressure is the plan known not to be short,
// unknown is no reading to judge by.
func ReadPressureKnown(limitsPath string, figures boundaryledger.Dir, now time.Time) (*Pressure, bool) {
	if r, ok, err := claudelimits.Read(limitsPath); err == nil && ok && now.Sub(r.ObservedAt) <= pressureLimitsFreshFor {
		// A fresh status line reading is the vendor's own, so it answers
		// even when it finds no pressure.
		p, _ := PressureFromLimits(r, now)
		return p, true
	}
	if fig, ok, err := figures.GetFigures(); err == nil && ok && now.Sub(fig.ObservedAt) <= pressureFiguresFreshFor {
		p, _ := PressureFromFigures(fig, now)
		return p, len(fig.PlanLeftPct["anthropic"]) > 0
	}
	return nil, false
}

// PressureFromLimits judges the status line's 5h and weekly windows by
// plans.WindowPressure and returns the tightest pressed one.
func PressureFromLimits(r claudelimits.Reading, now time.Time) (*Pressure, bool) {
	if r.ObservedAt.IsZero() || now.Sub(r.ObservedAt) > pressureLimitsFreshFor {
		return nil, false
	}
	p := plans.WindowPressure("Claude", limitsWindows(r), now)
	if p == nil {
		return nil, false
	}
	p.ObservedAt, p.FreshUntil = r.ObservedAt.UTC(), r.ObservedAt.Add(pressureLimitsFreshFor).UTC()
	p.Source = "claude_code_statusline"
	return p, true
}

// PressureFromFigures judges the daemon's figures for the Claude plan. They
// carry no reset, so only the floor applies.
func PressureFromFigures(fig boundaryledger.Figures, now time.Time) (*Pressure, bool) {
	if fig.ObservedAt.IsZero() || now.Sub(fig.ObservedAt) > pressureFiguresFreshFor {
		return nil, false
	}
	p := plans.WindowPressure("Claude", figuresWindows(fig), now)
	if p == nil {
		return nil, false
	}
	p.ObservedAt, p.FreshUntil = fig.ObservedAt.UTC(), fig.ObservedAt.Add(pressureFiguresFreshFor).UTC()
	p.Source = "daemon_glance"
	return p, true
}

// limitsWindows are the status line's 5h and weekly windows with their
// lengths and resets.
func limitsWindows(r claudelimits.Reading) []plans.VendorWindow {
	var windows []plans.VendorWindow
	for _, w := range []struct {
		name string
		win  *claudelimits.Window
		dur  time.Duration
	}{{"5h", r.FiveHour, 5 * time.Hour}, {"week", r.SevenDay, 7 * 24 * time.Hour}} {
		// A window without a reset cannot be told apart from one that
		// has reset since the reading.
		if w.win == nil || w.win.ResetsAt <= 0 {
			continue
		}
		windows = append(windows, plans.VendorWindow{
			Name: w.name, UsedPct: w.win.UsedPct, Duration: w.dur, ResetsAt: time.Unix(w.win.ResetsAt, 0).UTC(),
		})
	}
	return windows
}

// figuresWindows are the Claude plan's windows in the daemon's figures:
// the share left, with no reset.
func figuresWindows(fig boundaryledger.Figures) []plans.VendorWindow {
	var windows []plans.VendorWindow
	for _, name := range []string{"5h", "week"} {
		if left, ok := fig.PlanLeftPct["anthropic"][name]; ok {
			windows = append(windows, plans.VendorWindow{Name: name, UsedPct: 100 - left})
		}
	}
	return windows
}
