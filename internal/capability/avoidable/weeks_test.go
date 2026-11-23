package avoidable

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// fakeLog is an event log at pos whose changes since any earlier position
// are changed (attributable unless placeable is false).
type fakeLog struct {
	pos       sqlite.Position
	changed   []time.Time
	placeable bool
}

func (f *fakeLog) Position(context.Context) (sqlite.Position, error) { return f.pos, nil }
func (f *fakeLog) ChangedTimes(context.Context, eventschema.EventType, sqlite.Position, sqlite.Position) ([]time.Time, bool, error) {
	return f.changed, f.placeable, nil
}

// counter computes a week as its start's day of the year, and counts the
// weeks it was asked for.
type counter struct{ asked map[time.Time]int }

func (c *counter) figures(_ context.Context, start time.Time, _ int64) (WeekFigs, error) {
	if c.asked == nil {
		c.asked = map[time.Time]int{}
	}
	c.asked[start]++
	return WeekFigs{Start: start, OversizedTokens: int64(start.YearDay()) * 1000, RejectRatePct: 10, Prompts: 100}, nil
}

// Monday 2026-11-16 00:00 UTC is a week start.
var weeksNow = time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)

func TestWeekStartIsMondayUTC(t *testing.T) {
	for _, in := range []time.Time{
		time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 18, 23, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 22, 23, 59, 0, 0, time.UTC),
	} {
		if got := weekStart(in); !got.Equal(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("weekStart(%s) = %s", in, got)
		}
	}
}

// Without a setting the rollup keeps the last MinWeeks finished weeks; with
// one it keeps MinWeeks either side of it, leaving out the week the setting
// fell in.
func TestWantedWeeks(t *testing.T) {
	monday := func(d int) time.Time { return time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 7*d) }
	tests := []struct {
		name  string
		setAt time.Time
		want  []time.Time
	}{
		{"no setting: the last four finished weeks", time.Time{}, []time.Time{monday(-4), monday(-3), monday(-2), monday(-1)}},
		{"set mid-week six weeks ago: four before, its week left out, the finished weeks after",
			monday(-6).Add(50 * time.Hour),
			[]time.Time{monday(-10), monday(-9), monday(-8), monday(-7), monday(-5), monday(-4), monday(-3), monday(-2), monday(-1)}},
		{"set on a week start two weeks ago", monday(-2),
			[]time.Time{monday(-6), monday(-5), monday(-4), monday(-3), monday(-2), monday(-1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wantedWeeks(tt.setAt, weeksNow)
			if len(got) != len(tt.want) {
				t.Fatalf("weeks = %v; want %v", got, tt.want)
			}
			for i := range got {
				if !got[i].Equal(tt.want[i]) {
					t.Fatalf("weeks = %v; want %v", got, tt.want)
				}
			}
		})
	}
}

// A refresh computes each wanted week once; with the log where it was it
// computes nothing; a change it can place recomputes only the week it
// touched; one it cannot place recomputes every week.
func TestRefreshWeeksComputesOnlyWhatChanged(t *testing.T) {
	dir := t.TempDir()
	log := &fakeLog{pos: sqlite.Position{Rowid: 10, Seq: 1}, placeable: true}
	c := &counter{}
	refresh := func() Weeks {
		t.Helper()
		w, err := RefreshWeeks(context.Background(), log, c.figures, dir, 0, time.Time{}, weeksNow)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	total := func() int {
		n := 0
		for _, v := range c.asked {
			n += v
		}
		return n
	}
	if w := refresh(); len(w.Weeks) != 4 || total() != 4 {
		t.Fatalf("first refresh: %d weeks, %d computed; want 4 and 4", len(w.Weeks), total())
	}
	refresh()
	if total() != 4 {
		t.Fatalf("an unchanged log recomputed %d weeks", total()-4)
	}
	touched := weekStart(weeksNow).AddDate(0, 0, -14)
	log.pos, log.changed = sqlite.Position{Rowid: 11, Seq: 1}, []time.Time{touched.Add(30 * time.Hour)}
	refresh()
	if total() != 5 || c.asked[touched] != 2 {
		t.Fatalf("a placed change recomputed %d weeks (touched week %d times); want only the touched week", total()-4, c.asked[touched])
	}
	log.pos, log.placeable = sqlite.Position{Rowid: 12, Seq: 2}, false
	refresh()
	if total() != 9 {
		t.Fatalf("an unplaceable change recomputed %d weeks; want every one", total()-5)
	}
	// A moved line measures every week against the new line.
	if _, err := RefreshWeeks(context.Background(), log, c.figures, dir, 300_000, time.Time{}, weeksNow); err != nil {
		t.Fatal(err)
	}
	if total() != 13 {
		t.Fatalf("a moved line recomputed %d weeks; want every one", total()-9)
	}
}

// The rollup's judgement compares the weeks either side of the setting,
// skipping weeks with no band to judge rejects in.
func TestWeeksJudgeAroundTheSetting(t *testing.T) {
	monday := func(d int) time.Time { return weekStart(weeksNow).AddDate(0, 0, 7*d) }
	w := Weeks{SetAt: monday(-4)}
	for i := -8; i < 0; i++ {
		f := WeekFigs{Start: monday(i), OversizedTokens: 900_000, RejectRatePct: 15, Prompts: 100}
		if i >= -4 {
			f.OversizedTokens, f.RejectRatePct = 300_000+int64(i)*1000, 8
		}
		if i == -4 {
			f.Prompts = 0
		}
		w.Weeks = append(w.Weeks, f)
	}
	v, from, ok := w.Judge()
	if !ok || !from.Equal(monday(-4)) {
		t.Fatalf("judge = ok %v from %s", ok, from)
	}
	if v.Realized {
		t.Errorf("realized with three judged weeks after the setting: %s", v.Reason)
	}
	if _, _, ok := (Weeks{}).Judge(); ok {
		t.Error("judged a rollup with no setting")
	}
}

// WeekFigures is the reference: its reject rate is the trusted bands' rate
// over the week's own instructions, as the waste report reads them.
func TestWeekFiguresReadsTheWeeksRecords(t *testing.T) {
	start := weekStart(weeksNow).AddDate(0, 0, -7)
	var asked [2]time.Time
	d := Deps{
		Turns: func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return nil, nil },
		Dirs:  func(time.Time) map[string]sessiondirs.Dir { return map[string]sessiondirs.Dir{} },
		Records: func(_ context.Context, since, until time.Time) ([]agentdx.Record, error) {
			asked = [2]time.Time{since, until}
			return nil, nil
		}}
	f, err := WeekFigures(context.Background(), d, start, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !asked[0].Equal(start) || !asked[1].Equal(start.AddDate(0, 0, 7)) {
		t.Errorf("read records over %v; want the week", asked)
	}
	if f.Prompts != 0 || f.OversizedTokens != 0 || !f.Start.Equal(start) {
		t.Errorf("figures = %+v; want an empty week", f)
	}
}

// A change the log counts only as Older (an update or delete of an event
// past the change log's horizon, such as the retention prune) can touch
// only the weeks that reach back past that horizon: the recent weeks stay
// as stored. The daemon recomputed every week after each prune.
func TestRefreshWeeksAfterAnOldChangeRecomputesOnlyOldWeeks(t *testing.T) {
	dir := t.TempDir()
	log := &fakeLog{pos: sqlite.Position{Rowid: 10, Seq: 1}, placeable: true}
	c := &counter{}
	setAt := weeksNow.AddDate(0, 0, -70)
	refresh := func() Weeks {
		t.Helper()
		w, err := RefreshWeeks(context.Background(), log, c.figures, dir, 0, setAt, weeksNow)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	w := refresh()
	horizon := weeksNow.Add(-sqlite.ChangeLogHorizon)
	var old int
	for _, f := range w.Weeks {
		if f.Start.Before(horizon) {
			old++
		}
	}
	if old == 0 || old == len(w.Weeks) {
		t.Fatalf("%d of %d weeks reach past the horizon; the test needs some of each", old, len(w.Weeks))
	}
	first := len(c.asked)
	log.pos.Older = 3
	refresh()
	recomputed := 0
	for start, n := range c.asked {
		if n > 1 {
			recomputed++
			if !start.Before(horizon) {
				t.Errorf("week %s, inside the change log's horizon, was recomputed", start.Format(time.DateOnly))
			}
		}
	}
	if recomputed != old || len(c.asked) != first {
		t.Errorf("recomputed %d weeks, want the %d reaching past the horizon", recomputed, old)
	}
}
