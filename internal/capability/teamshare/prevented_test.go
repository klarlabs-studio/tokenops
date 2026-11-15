package teamshare

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// preventedDays is two full UTC weeks of day reports from Monday
// 2026-10-26, each day refusing one re-read (100 tokens, $0.10) and the
// first two days of the first week each counting the same weekly window
// as a lockout avoided.
func preventedDays() []avoidable.Day {
	start := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	week := avoidable.AvoidedWindow{Provider: "anthropic", Window: "week", ResetsAt: start.AddDate(0, 0, 5), BlockedMinutes: 300}
	out := make([]avoidable.Day, 0, 14)
	for i := range 14 {
		ms := []avoidable.Mechanism{
			{Key: avoidable.MechReadGuard, Count: 1, Tokens: 100, USD: measurement.Estimated(0.1, "test", "")},
			{Key: avoidable.MechBoundary, USD: measurement.Unknown("not priced")},
		}
		if i < 2 {
			ms = append(ms, avoidable.Mechanism{Key: avoidable.MechLockout, Count: 1, BlockedMinutes: 300,
				Windows: []avoidable.AvoidedWindow{week}, USD: measurement.Unknown("counterfactual")})
		}
		out = append(out, avoidable.Day{Start: start.AddDate(0, 0, i), Report: avoidable.Report{Prevented: avoidable.Prevented{Mechanisms: ms}}})
	}
	return out
}

func coveredFrom(ds []avoidable.Day) map[string]bool {
	out := map[string]bool{}
	for _, d := range ds {
		out[d.Start.Format(teamwire.DayLayout)] = true
	}
	return out
}

// Only weeks fully covered and finished three days ago are sent; their
// totals add the days, a window counted on two days counts once, and a
// mechanism that did nothing has no row.
func TestPreventedRows(t *testing.T) {
	ds := preventedDays()
	// The second week ends 2026-11-09; the first, 2026-11-02.
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	weeks, rows := PreventedRows(ds, coveredFrom(ds), now)
	if len(weeks) != 1 || weeks[0] != "2026-10-26" {
		t.Fatalf("weeks = %v; want only the first, finished three days", weeks)
	}
	want := map[string]teamwire.PreventedWeek{
		teamwire.MechanismReadGuard: {Week: "2026-10-26", Mechanism: teamwire.MechanismReadGuard, Count: 7, Tokens: 700},
		teamwire.MechanismLockout:   {Week: "2026-10-26", Mechanism: teamwire.MechanismLockout, Count: 1, BlockedMinutes: 300},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v", rows)
	}
	for _, r := range rows {
		w, ok := want[r.Mechanism]
		if !ok || r.Week != w.Week || r.Count != w.Count || r.Tokens != w.Tokens || r.BlockedMinutes != w.BlockedMinutes {
			t.Errorf("row %+v; want %+v", r, w)
		}
		switch r.Mechanism {
		case teamwire.MechanismReadGuard:
			if r.USD == nil || round4(*r.USD) != 0.7 {
				t.Errorf("read guard dollars = %v; want 0.70", r.USD)
			}
		default:
			if r.USD != nil {
				t.Errorf("%s priced at %v; it is not priced", r.Mechanism, *r.USD)
			}
		}
		if err := r.Validate(); err != nil {
			t.Errorf("row %+v invalid: %v", r, err)
		}
	}
	// A week missing a covered day is not complete and is not sent.
	partial := coveredFrom(ds)
	delete(partial, "2026-10-28")
	if weeks, _ := PreventedRows(ds, partial, now); len(weeks) != 0 {
		t.Errorf("a partly covered week was sent: %v", weeks)
	}
}

// Every mechanism the waste report counts is one the upload may name.
func TestEveryPreventedMechanismCrosses(t *testing.T) {
	for _, k := range []string{avoidable.MechReadGuard, avoidable.MechFmt, avoidable.MechAutopilot, avoidable.MechBoundary,
		avoidable.MechLoopBreaker, avoidable.MechCompaction, avoidable.MechLockout} {
		if !teamwire.ValidMechanism(k) {
			t.Errorf("%s is not a teamwire mechanism", k)
		}
	}
}

// A schema 5 upload carries the finished weeks' prevented figures and
// still validates as a whole; the same machine sends none to a schema 4
// server.
func TestSchema5UploadCarriesPrevented(t *testing.T) {
	d := deps()
	ds := preventedDays()
	d.FinOps = FinOps{Waste: func(context.Context, TurnReader, time.Time, time.Time) ([]avoidable.Day, error) { return ds, nil }}
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	u, notes := Build(context.Background(), d, Options{Days: 16, RepoNames: RepoRemote, Schema: teamwire.SchemaPrevented}, at)
	if len(notes.All()) > 0 {
		t.Fatalf("notes: %v", notes.All())
	}
	if u.Schema != teamwire.SchemaPrevented || len(u.PreventedWeeks) != 1 || len(u.Prevented) == 0 {
		t.Fatalf("schema 5 upload: schema %d, weeks %v, rows %d", u.Schema, u.PreventedWeeks, len(u.Prevented))
	}
	if err := u.Validate(); err != nil {
		t.Fatalf("schema 5 upload does not validate: %v", err)
	}
	u4, _ := Build(context.Background(), d, Options{Days: 16, RepoNames: RepoRemote, Schema: teamwire.SchemaReverts}, at)
	if u4.Schema != teamwire.SchemaReverts || u4.Prevented != nil {
		t.Errorf("schema 4 upload carries prevented figures: %+v", u4.Prevented)
	}
}

// A lockout window counted on two days contributes its minutes once. Each
// further window adds its own minutes, and the week's total is capped at
// the bound a row may carry, so a busy week's upload still validates.
func TestPreventedBlockedMinutesCountEachWindowOnce(t *testing.T) {
	start := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	session := avoidable.AvoidedWindow{Provider: "anthropic", Window: "session", ResetsAt: start.Add(5 * time.Hour), BlockedMinutes: 120}
	lockout := func(ws ...avoidable.AvoidedWindow) avoidable.Mechanism {
		var sum float64
		for _, w := range ws {
			sum += w.BlockedMinutes
		}
		return avoidable.Mechanism{Key: avoidable.MechLockout, Count: len(ws), BlockedMinutes: sum, Windows: ws}
	}
	week := avoidable.AvoidedWindow{Provider: "anthropic", Window: "week", ResetsAt: start.AddDate(0, 0, 5), BlockedMinutes: 300}
	ds := make([]avoidable.Day, 0, 7)
	for i := range 7 {
		ms := []avoidable.Mechanism{}
		switch i {
		case 0, 1:
			ms = append(ms, lockout(week))
		case 2:
			ms = append(ms, lockout(week, session))
		}
		ds = append(ds, avoidable.Day{Start: start.AddDate(0, 0, i), Report: avoidable.Report{Prevented: avoidable.Prevented{Mechanisms: ms}}})
	}
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	_, rows := PreventedRows(ds, coveredFrom(ds), now)
	if len(rows) != 1 || rows[0].Mechanism != teamwire.MechanismLockout {
		t.Fatalf("rows = %+v", rows)
	}
	if got := rows[0].BlockedMinutes; got != 420 || rows[0].Count != 2 {
		t.Errorf("blocked minutes %d, count %d; want 420 (the week and the session window once each), count 2", got, rows[0].Count)
	}

	// Many distinct windows in one week sum past a week; the row is capped.
	many := make([]avoidable.AvoidedWindow, 0, 40)
	for i := range 40 {
		many = append(many, avoidable.AvoidedWindow{Provider: "anthropic", Window: "session", ResetsAt: start.Add(time.Duration(i) * time.Hour), BlockedMinutes: 300})
	}
	ds = []avoidable.Day{{Start: start, Report: avoidable.Report{Prevented: avoidable.Prevented{Mechanisms: []avoidable.Mechanism{lockout(many...)}}}}}
	for i := 1; i < 7; i++ {
		ds = append(ds, avoidable.Day{Start: start.AddDate(0, 0, i)})
	}
	_, rows = PreventedRows(ds, coveredFrom(ds), now)
	if len(rows) != 1 || rows[0].BlockedMinutes != teamwire.MaxBlockedMinutes {
		t.Fatalf("capped row = %+v; want blocked minutes %d", rows, teamwire.MaxBlockedMinutes)
	}
	if err := rows[0].Validate(); err != nil {
		t.Errorf("capped row does not validate: %v", err)
	}
}
