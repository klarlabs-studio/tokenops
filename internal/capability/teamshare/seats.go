package teamshare

import (
	"context"
	"math"
	"slices"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// Schema 6's seat value is the figures `tokenops plan advise` already
// shows the member, reduced to enumerations, catalog plans, dates and
// whole numbers: the plan-covered usage at API list price (plans.ListValue),
// the replay's options, the plans bound, and the longest window's
// projection at its reset (plans.ProjectAtReset). Nothing of a 5-hour
// window and no instant is sent.

// weekLength is the length of a weekly plan window, the one kind of
// longest window a forward snapshot describes so far.
const weekLength = 7 * 24 * time.Hour

// AddPlanValue sets each plan day's value: the provider's plan-covered
// turns on that day at API list price and the turns with no rate, as
// plans.ListValue gives the replay. A nil price leaves the rows unpriced.
func AddPlanValue(rows []teamwire.PlanDay, envs []*eventschema.Envelope, price plans.Pricer, now time.Time) {
	if price == nil || len(rows) == 0 {
		return
	}
	addPlanValue(rows, plans.DigestOf(envs, price, time.Time{}), now)
}

// addPlanValue is AddPlanValue from the events' digest, built with the
// price.
func addPlanValue(rows []teamwire.PlanDay, use plans.UseDigest, now time.Time) {
	for i := range rows {
		start, err := time.Parse(teamwire.DayLayout, rows[i].Day)
		if err != nil {
			continue
		}
		end := dayEnd(start, now)
		usd, unpriced := use.ListValue(rows[i].Provider, start, end)
		rows[i].PlanValueUSD, rows[i].PlanUnpricedTurns = &usd, int64(unpriced)
	}
}

// PlanBindings is a row for every plan bound that the catalog knows, used
// or not: an idle seat has no plan day and no advice, and would otherwise
// not be seen. Since is the UTC date the plan history has the plan in
// force from, a price change or a retired name on the way not counting as
// a new plan; shared are the providers the member says they share.
func PlanBindings(bound map[string]string, history plans.History, shared []string, now time.Time) []teamwire.PlanBinding {
	provs := make([]string, 0, len(bound))
	for p := range bound {
		provs = append(provs, p)
	}
	sort.Strings(provs)
	var out []teamwire.PlanBinding
	for _, p := range provs {
		plan, _ := plans.ResolveAlias(bound[p])
		if !teamwire.ValidPlan(p, plan) {
			continue
		}
		row := teamwire.PlanBinding{Provider: p, Plan: plan, Shared: slices.Contains(shared, p)}
		if since := planSince(history, p, plan, now); !since.IsZero() {
			row.Since = since.UTC().Format(teamwire.DayLayout)
		}
		out = append(out, row)
	}
	return out
}

// planSince is when plan has been in force for provider without a break,
// by the plan history; zero when the history does not say.
func planSince(h plans.History, provider, plan string, now time.Time) time.Time {
	periods := h.Periods(provider, time.Time{}, now, plan)
	var since time.Time
	for i := len(periods) - 1; i >= 0; i-- {
		p := periods[i]
		if name, _ := plans.ResolveAlias(p.Plan); name != plan || (!since.IsZero() && !p.To.Equal(since)) {
			break
		}
		since = p.From
	}
	return since
}

// PlanWindows is where each bound plan's longest window is headed by its
// reset, on the forecast the member's own surfaces show
// (headroom.ForecastWindows, plans.LongestWindow). Only a weekly window is
// described: a plan whose longest window is shorter has no row.
func PlanWindows(ctx context.Context, r headroom.Reader, bound map[string]string, now time.Time) []teamwire.PlanWindow {
	provs := make([]string, 0, len(bound))
	for p := range bound {
		provs = append(provs, p)
	}
	sort.Strings(provs)
	var out []teamwire.PlanWindow
	for _, p := range provs {
		w, ok := plans.LongestWindow(headroom.ForecastWindows(ctx, r, p, now), now)
		if !ok || w.Duration != weekLength {
			continue
		}
		out = append(out, planWindow(p, w, now))
	}
	return out
}

// planWindow is w's forward snapshot: whole points, the shortfall in
// 6-hour steps and the reset's date.
func planWindow(provider string, w plans.VendorWindow, now time.Time) teamwire.PlanWindow {
	f := w.Forecast(now)
	row := teamwire.PlanWindow{Provider: provider, Window: teamwire.WindowWeekly,
		UsedPct: int64(clamp(math.Round(w.UsedPct), 0, 100)), ResetsOn: w.ResetsAt.UTC().Format(teamwire.DayLayout),
		Basis: teamwire.BasisAverage}
	if f.Basis == plans.BasisRecent {
		row.Basis = teamwire.BasisRecent
	}
	pct, ok := plans.ProjectAtReset(w, now)
	if !ok {
		row.TooEarly = true
		return row
	}
	row.ProjectedPctAtReset = int64(clamp(math.Round(pct), 0, teamwire.MaxProjectedPct))
	row.RunsOut = !f.LastsToReset
	if row.RunsOut {
		short := (w.ResetsAt.Sub(now) - f.RunsOutIn).Hours()
		step := float64(teamwire.ShortHoursStep)
		row.ShortHours = int64(clamp(math.Round(short/step)*step, 0, teamwire.MaxShortHours))
	}
	return row
}

// dayEnd is the last instant of the UTC day from start, or now while the
// day is not over: the span PlanDays measures the day over.
func dayEnd(start, now time.Time) time.Time {
	if end := start.Add(24*time.Hour - time.Nanosecond); end.Before(now) {
		return end
	}
	return now
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// adviceOptions is every option a replay priced, as kinds, catalog plans
// and figures; the display names and the windows' names stay on the
// machine. At most teamwire.MaxAdviceOptions, in the replay's order.
func adviceOptions(os []plans.Option) []teamwire.AdviceOption {
	out := make([]teamwire.AdviceOption, 0, min(len(os), teamwire.MaxAdviceOptions))
	for _, o := range os {
		if len(out) == teamwire.MaxAdviceOptions {
			break
		}
		row := teamwire.AdviceOption{Kind: o.Kind, Plan: o.Plan, Lockouts: int64(o.Lockouts), BlockedMinutes: int64(o.BlockedMinutes)}
		if o.MonthlyUSD != nil {
			row.MonthlyUSD = ptrFloat(*o.MonthlyUSD)
		}
		if o.PeakPct != nil {
			row.PeakPct = ptrFloat(math.Round(*o.PeakPct))
		}
		out = append(out, row)
	}
	return out
}

func ptrFloat(v float64) *float64 { return &v }
