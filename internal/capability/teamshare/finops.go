package teamshare

import (
	"context"
	"fmt"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// The FinOps sections of a schema 2 upload (#685) are the figures the
// machine already computes for its own surfaces — plan use (#657), the
// waste taxonomy (#655), the realized-savings ledger (#656) and the plan
// advice (#653) — read through the same capabilities, filed by day and,
// for waste, by repository and kind of work, and reduced to teamwire's
// enumerations and sums. Allocation tags (#658) are not read here: they
// stay on the machine.

// FinOps are the readers of a schema 2 upload's sections. A nil field
// leaves its sections out.
type FinOps struct {
	// Plans reads the plan bindings, the recorded prompt events (window
	// readings and turns) and the plan history: plan days and plan advice.
	Plans func() headroom.Deps
	// Waste is the waste report and realized savings for each UTC day from
	// since's to until, read from turns when it is set.
	Waste func(ctx context.Context, turns TurnReader, since, until time.Time) ([]avoidable.Day, error)
}

// finOpsFor reads this machine's plans, event store and rate card, and
// the waste report through waste. A nil engine leaves the money the rate
// card prices out.
func finOpsFor(cfg config.Config, store *sqlite.Store, eng *spend.Engine, waste avoidable.Deps) FinOps {
	return FinOps{
		Plans: func() headroom.Deps {
			d := headroom.Deps{Config: &cfg, Reader: store, PlanHistory: planhistory.LoadDefault}
			if eng != nil {
				d.Price = eng.ComputeAt
			}
			return d
		},
		Waste: func(ctx context.Context, turns TurnReader, since, until time.Time) ([]avoidable.Day, error) {
			d := waste
			if turns != nil {
				d.Turns = turns
			}
			return avoidable.Days(ctx, d, since, until)
		},
	}
}

// windowLookback is how long before the first covered day window
// readings are read: a weekly window that resets on that day filled up
// over the week before.
const windowLookback = 7 * 24 * time.Hour

// addFinOps fills u's schema 2 sections and marks it schema 2. A reader
// that fails leaves its sections empty with a warning; a row that would
// fail validation is dropped with one, so the buckets are still sent.
func addFinOps(ctx context.Context, f FinOps, turns TurnReader, took func(string, time.Duration), u *teamwire.Upload, repoOf map[string]string, kindOf kinds, now time.Time) []string {
	u.Schema = teamwire.SchemaFinOps
	first, err := time.Parse(teamwire.DayLayout, u.Days[0])
	if err != nil {
		return []string{"finops: " + err.Error()}
	}
	covered := map[string]bool{}
	for _, d := range u.Days {
		covered[d] = true
	}
	var warnings []string
	if f.Plans != nil {
		d := f.Plans()
		if d.Config != nil && len(d.Config.Plans) > 0 && d.Reader != nil {
			var history plans.History
			if d.PlanHistory != nil {
				if h, err := d.PlanHistory(); err == nil {
					history = h
				}
			}
			done := lap(took, "plan days")
			envs, err := d.Reader.ReadEvents(ctx, eventschema.EventTypePrompt, first.Add(-windowLookback))
			done()
			if err != nil {
				warnings = append(warnings, "plan days: "+err.Error())
			} else {
				u.PlanDays = PlanDays(envs, d.Config.Plans, history, u.Days, now)
			}
			done = lap(took, "plan advice")
			res, err := headroom.Advise(ctx, d, headroom.AdviceQuery{}, now)
			done()
			if err != nil {
				warnings = append(warnings, "plan advice: "+err.Error())
			} else {
				u.PlanAdvice = AdviceRows(res.Providers)
			}
		}
	}
	if f.Waste != nil {
		done := lap(took, "waste")
		ds, err := f.Waste(ctx, turns, first, now)
		done()
		if err != nil {
			warnings = append(warnings, "waste: "+err.Error())
		} else {
			u.Waste = WasteRows(ds, repoOf, kindOf.at, covered)
			u.Savings = SavingsRows(ds, covered)
		}
	}
	return append(warnings, dropInvalid(u)...)
}

// dropInvalid removes the rows that would fail validation, saying how
// many: a figure the boundary refuses is a bug here, not a reason to hold
// back the rest.
func dropInvalid(u *teamwire.Upload) []string {
	var warnings []string
	note := func(section string, n int) {
		if n > 0 {
			warnings = append(warnings, fmt.Sprintf("%s: %d rows failed validation and were left out", section, n))
		}
	}
	var n int
	u.PlanDays, n = keepValid(u.PlanDays, teamwire.PlanDay.Validate)
	note("plan days", n)
	u.Waste, n = keepValid(u.Waste, teamwire.Waste.Validate)
	note("waste", n)
	u.Savings, n = keepValid(u.Savings, teamwire.SavingsDay.Validate)
	note("savings", n)
	u.PlanAdvice, n = keepValid(u.PlanAdvice, teamwire.PlanAdvice.Validate)
	note("plan advice", n)
	return warnings
}

func keepValid[T any](rows []T, valid func(T) error) ([]T, int) {
	out := rows[:0]
	for _, r := range rows {
		if valid(r) == nil {
			out = append(out, r)
		}
	}
	return out, len(rows) - len(out)
}

// PlanDays is each bound plan's use on each of days: utilization and
// coverage as the scorecard measures them (plans.PlanUseOn) and the
// windows that ran out (plans.LockoutsByDay). The plan of a day is the one
// in force at its end, from the plan history. A day with nothing measured
// for a plan has no row.
func PlanDays(envs []*eventschema.Envelope, bound map[string]string, history plans.History, days []string, now time.Time) []teamwire.PlanDay {
	provs := make([]string, 0, len(bound))
	for p := range bound {
		provs = append(provs, p)
	}
	sort.Strings(provs)
	locks := map[string]map[string]plans.DayLockouts{}
	for _, p := range provs {
		locks[p] = plans.LockoutsByDay(plans.QuotaReadingsFrom(envs, p, time.Time{}, now), now)
	}
	var out []teamwire.PlanDay
	for _, day := range days {
		start, err := time.Parse(teamwire.DayLayout, day)
		if err != nil || start.After(now) {
			continue
		}
		end := start.Add(24*time.Hour - time.Nanosecond)
		if end.After(now) {
			end = now
		}
		use := plans.PlanUseOn(envs, start, end)
		for _, p := range provs {
			current, _ := plans.ResolveAlias(bound[p])
			plan, _ := plans.ResolveAlias(history.At(p, end, current))
			if !teamwire.ValidPlan(p, plan) {
				continue
			}
			u, _ := use.For(p)
			l := locks[p][day]
			if u.UtilizationPct == nil && u.CoveragePct == nil && l.Lockouts == 0 && l.BlockedMinutes == 0 {
				continue
			}
			row := teamwire.PlanDay{Day: day, Provider: p, Plan: plan, UtilizationPct: u.UtilizationPct,
				CoveragePct: u.CoveragePct, PlanTokens: u.PlanTokens, BilledTokens: u.BilledTokens,
				Lockouts: int64(l.Lockouts), BlockedMinutes: int64(min(l.BlockedMinutes, 24*60))}
			for _, w := range u.Windows {
				if w.Label == u.UtilizationWindow {
					row.UtilizationWindows = int64(w.Windows)
				}
			}
			out = append(out, row)
		}
	}
	return out
}

// WasteRows files each day's attributed waste under its day, repository
// and kind of work, per class, and sums.
func WasteRows(ds []avoidable.Day, repoOf map[string]string, kindAt func(session string, at time.Time) taskclass.Kind, covered map[string]bool) []teamwire.Waste {
	type key struct{ day, repo, kind, class string }
	rows := map[key]*teamwire.Waste{}
	for _, d := range ds {
		for _, tw := range d.Turns {
			day := tw.Turn.At.UTC().Format(teamwire.DayLayout)
			if !covered[day] || tw.Tokens <= 0 {
				continue
			}
			repo := repoOf[tw.Turn.SessionID]
			if repo == "" {
				repo = teamwire.RepoNone
			}
			k := key{day, repo, string(kindAt(tw.Turn.SessionID, tw.Turn.At)), string(tw.Class)}
			r := rows[k]
			if r == nil {
				r = &teamwire.Waste{Day: k.day, Repo: k.repo, Kind: k.kind, Class: k.class}
				rows[k] = r
			}
			r.Turns++
			r.Tokens += tw.Tokens
			r.APIEquivalentUSD += tw.APIEquivalentUSD
		}
	}
	out := make([]teamwire.Waste, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.Day != y.Day {
			return x.Day < y.Day
		}
		if x.Repo != y.Repo {
			return x.Repo < y.Repo
		}
		if x.Kind != y.Kind {
			return x.Kind < y.Kind
		}
		return x.Class < y.Class
	})
	return out
}

// SavingsRows is each day's realized savings beside what stayed
// avoidable. A day with nothing to report has no row.
func SavingsRows(ds []avoidable.Day, covered map[string]bool) []teamwire.SavingsDay {
	var out []teamwire.SavingsDay
	for _, d := range ds {
		day := d.Start.UTC().Format(teamwire.DayLayout)
		if !covered[day] {
			continue
		}
		r := d.Report.Realized
		row := teamwire.SavingsDay{Day: day, Interventions: int64(d.Savings.Interventions), Held: int64(d.Savings.Held),
			RefusedRereads: int64(r.RefusedRereads), RefusedTokens: r.RefusedTokens, RefusedUSD: r.RefusedUSD,
			RealizedUSD: r.USD, AvoidableUSD: d.Report.Avoidable.APIEquivalentUSD}
		if v, ok := r.HoldoutUSD.Amount(); ok {
			row.HoldoutUSD = &v
		}
		if row == (teamwire.SavingsDay{Day: day}) {
			continue
		}
		out = append(out, row)
	}
	return out
}

// AdviceRows is each replay that recommends something, reduced to the
// verdict, the tier and the figures that back it. The summary and the
// caveats are prose and stay on the machine.
func AdviceRows(advs []plans.Advice) []teamwire.PlanAdvice {
	var out []teamwire.PlanAdvice
	for _, a := range advs {
		rec := a.Recommendation
		if rec == nil {
			continue
		}
		row := teamwire.PlanAdvice{Provider: a.Provider, Plan: a.Plan, Verdict: rec.Verdict, Kind: rec.Kind,
			RecommendedPlan: rec.Plan, MonthlyDeltaUSD: rec.SavingsUSD, Material: rec.Material,
			Confidence: a.Confidence, Days: a.Days, WindowsMeasured: int64(a.WindowsMeasured)}
		for _, o := range a.Options {
			if o.Current && o.Kind == plans.OptionPlan {
				row.Lockouts, row.BlockedMinutes = int64(o.Lockouts), int64(o.BlockedMinutes)
			}
			if o.Kind == rec.Kind && o.Plan == rec.Plan {
				row.RecommendedLockouts, row.RecommendedBlockedMinutes = int64(o.Lockouts), int64(o.BlockedMinutes)
			}
		}
		out = append(out, row)
	}
	return out
}
