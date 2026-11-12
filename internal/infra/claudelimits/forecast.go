package claudelimits

import (
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// planWindow is one of the plan-wide windows the status line reports.
type planWindow struct {
	// name is the window as a rule and the figures name it; label as the
	// burn-rate reading does.
	name, label string
	length      time.Duration
	pick        func(Reading) *Window
}

var planWindows = []planWindow{
	{"5h", "5-hour", 5 * time.Hour, func(r Reading) *Window { return r.FiveHour }},
	{"week", "weekly", 7 * 24 * time.Hour, func(r Reading) *Window { return r.SevenDay }},
}

// Windows are r's 5h and weekly windows that carry a reset, named "5h"
// and "week", each paced by the one forecast glance shows: the rate of the
// last hour and quarter hour, read from r's earlier readings, where they
// measure the window (plans.AssessBurn, plans.ApplyBurn); else the
// window's average so far. A window whose reset has passed is returned as
// read; callers judge it stale.
func (r Reading) Windows(now time.Time) []plans.VendorWindow {
	var out []plans.VendorWindow
	for _, pw := range planWindows {
		w := pw.pick(r)
		// A window without a reset cannot be told apart from one that
		// has reset since the reading.
		if w == nil || w.ResetsAt <= 0 {
			continue
		}
		vw := plans.VendorWindow{Name: pw.name, UsedPct: w.UsedPct, Duration: pw.length,
			ResetsAt: time.Unix(w.ResetsAt, 0).UTC(), ObservedAt: r.ObservedAt.UTC()}
		vw.Pace = vw.PaceAt(now)
		out = append(out, vw)
	}
	plans.ApplyBurn(out, r.burn(now), now)
	return out
}

// burn is the burn-rate reading of r's windows against its own earlier
// readings, taken an hour and a quarter hour before r, as the coach and
// glance read the event store's.
func (r Reading) burn(now time.Time) plans.BurnVerdict {
	cur := plans.QuotaReading{At: r.ObservedAt, Windows: plans.OpenAt(r.quotaWindows(), now)}
	if len(cur.Windows) == 0 {
		return plans.BurnVerdict{}
	}
	lookback := func(d time.Duration) *plans.QuotaReading {
		past, ok := r.Between(r.ObservedAt.Add(-2*d), r.ObservedAt.Add(-d))
		if !ok {
			return nil
		}
		return &plans.QuotaReading{At: past.ObservedAt, Windows: past.quotaWindows()}
	}
	return plans.AssessBurn(cur, lookback(plans.BurnLongLookback), lookback(plans.BurnShortLookback), now)
}

// quotaWindows are r's plan-wide windows as the burn-rate reading takes
// them.
func (r Reading) quotaWindows() []plans.QuotaWindow {
	var out []plans.QuotaWindow
	for _, pw := range planWindows {
		if w := pw.pick(r); w != nil && w.ResetsAt > 0 {
			out = append(out, plans.QuotaWindow{Label: pw.label, UsedPct: w.UsedPct,
				ResetsAt: time.Unix(w.ResetsAt, 0).UTC(), Duration: pw.length})
		}
	}
	return out
}
