package avoidable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/learning/compactwatch"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/internal/tempfile"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// The compaction setting is judged on the developer's own weeks (ADR
// 0021): the context carried above the line and the reject rate, per
// week, before and after the day it was set. Those weeks are a small
// stored rollup (ADR 0019): a finished week is computed once, from the
// same readers the waste report uses, and again only when a change in the
// event log touches it, the line it is measured against moves, or the
// rollup's version changes. Only the weeks the judgement needs are kept:
// the last MinWeeks finished ones, and MinWeeks either side of the
// setting.

// WeeksVersion is the rollup's layout and computation version. A rollup of
// another version is rebuilt. Version 2 measures each week's reject rate
// over that week's instructions; version 1 pooled every instruction from
// the week's start to the day it was computed.
const WeeksVersion = 2

const weeksFile = "compact-weeks.json"

// Weeks is the stored rollup.
type Weeks struct {
	Version int `json:"version"`
	// Position is where the event log stood when the weeks were computed.
	Position sqlite.Position `json:"position"`
	// Line is the context line every week is measured against; 0 measures
	// each against the agent's configured line.
	Line int64 `json:"line,omitempty"`
	// SetAt is when tokenops first set the compaction line, zero when it
	// has not.
	SetAt      time.Time  `json:"set_at,omitzero"`
	ComputedAt time.Time  `json:"computed_at"`
	Weeks      []WeekFigs `json:"weeks"`
}

// WeekFigs is one finished week's figures.
type WeekFigs struct {
	Start time.Time `json:"start"`
	// OversizedTokens is the waste class oversized_context in the week.
	OversizedTokens int64 `json:"oversized_tokens"`
	// RejectRatePct is the reject rate over the bands with enough
	// instructions to judge, and Prompts how many instructions it pools.
	RejectRatePct float64 `json:"reject_rate_pct"`
	Prompts       int     `json:"prompts"`
}

// WeekLog is the event log the rollup follows.
type WeekLog interface {
	Position(ctx context.Context) (sqlite.Position, error)
	ChangedTimes(ctx context.Context, t eventschema.EventType, from, to sqlite.Position) ([]time.Time, bool, error)
}

// weekStart is the Monday 00:00 UTC that starts t's week.
func weekStart(t time.Time) time.Time {
	t = t.UTC()
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

// judgedFrom is the first whole week after the setting: the week the
// setting fell in is neither before nor after it.
func judgedFrom(setAt time.Time) time.Time {
	w := weekStart(setAt)
	if w.Equal(setAt.UTC()) {
		return w
	}
	return w.AddDate(0, 0, 7)
}

// wantedWeeks are the finished weeks the judgement needs at now.
func wantedWeeks(setAt, now time.Time) []time.Time {
	cur := weekStart(now)
	want := map[time.Time]bool{}
	for i := 1; i <= compactwatch.MinWeeks; i++ {
		want[cur.AddDate(0, 0, -7*i)] = true
	}
	if !setAt.IsZero() {
		// The weeks before end where the setting's own week starts; a
		// setting made mid-week leaves that week out on both sides.
		from, beforeEnd := judgedFrom(setAt), weekStart(setAt)
		for i := range compactwatch.MinWeeks {
			want[beforeEnd.AddDate(0, 0, -7*(i+1))] = true
			if w := from.AddDate(0, 0, 7*i); w.Before(cur) {
				want[w] = true
			}
		}
		if beforeEnd.Before(from) {
			delete(want, beforeEnd)
		}
	}
	out := make([]time.Time, 0, len(want))
	for w := range want {
		out = append(out, w)
	}
	slices.SortFunc(out, func(a, b time.Time) int { return a.Compare(b) })
	return out
}

// WeekFigures computes one week's figures from scratch: the reference the
// rollup is checked against (ADR 0019, decision 5). line pins the context
// line the oversized class is measured against; 0 keeps the agent's own.
func WeekFigures(ctx context.Context, d Deps, start time.Time, line int64) (WeekFigs, error) {
	end := start.AddDate(0, 0, 7)
	in, err := gather(ctx, d, start, end)
	if err != nil {
		return WeekFigs{}, err
	}
	if line > 0 {
		in.Waste.CompactAtOverride = line
	}
	r := Build(in)
	out := WeekFigs{Start: start}
	for _, c := range r.Classes {
		if c.Class == waste.ClassOversized {
			out.OversizedTokens = c.Tokens
		}
	}
	out.RejectRatePct, out.Prompts = agentdx.JudgedRejectRate(in.bands)
	return out, nil
}

// RefreshWeeks brings the rollup in dir up to date at now and returns it:
// it computes the wanted weeks it does not hold, and those a change in the
// log since the last refresh touched, and drops the weeks no longer
// needed. figures computes one week (WeekFigures over the report's
// readers). With the log where it was and nothing new to compute, it reads
// and writes nothing but the position.
func RefreshWeeks(ctx context.Context, log WeekLog, figures func(ctx context.Context, start time.Time, line int64) (WeekFigs, error),
	dir string, line int64, setAt, now time.Time) (Weeks, error) {
	if log == nil || figures == nil {
		return Weeks{}, errors.New("compaction weeks: no event log to follow")
	}
	pos, err := log.Position(ctx)
	if err != nil {
		return Weeks{}, err
	}
	old, _ := ReadWeeks(dir)
	held := map[time.Time]WeekFigs{}
	if old != nil && old.Version == WeeksVersion && old.Line == line {
		for _, w := range old.Weeks {
			held[w.Start.UTC()] = w
		}
		dirty, all, err := touchedWeeks(ctx, log, old.Position, pos)
		if err != nil {
			return Weeks{}, err
		}
		for w := range held {
			if all || dirty[w] {
				delete(held, w)
			}
		}
	}
	out := Weeks{Version: WeeksVersion, Position: pos, Line: line, ComputedAt: now.UTC()}
	if !setAt.IsZero() {
		out.SetAt = setAt.UTC()
	}
	for _, w := range wantedWeeks(setAt, now) {
		f, ok := held[w]
		if !ok {
			if f, err = figures(ctx, w, line); err != nil {
				return Weeks{}, fmt.Errorf("compaction week %s: %w", w.Format(time.DateOnly), err)
			}
		}
		out.Weeks = append(out.Weeks, f)
	}
	return out, writeWeeks(dir, out)
}

// touchedWeeks is the weeks a change in the log between from and to
// touched; all is true when a change cannot be placed in time, or there is
// no position to compare with.
func touchedWeeks(ctx context.Context, log WeekLog, from, to sqlite.Position) (map[time.Time]bool, bool, error) {
	if from == (sqlite.Position{}) || from.Older != to.Older {
		return nil, true, nil
	}
	if from == to {
		return nil, false, nil
	}
	times, attributable, err := log.ChangedTimes(ctx, eventschema.EventTypePrompt, from, to)
	if err != nil {
		return nil, false, err
	}
	if !attributable {
		return nil, true, nil
	}
	out := map[time.Time]bool{}
	for _, t := range times {
		out[weekStart(t)] = true
	}
	return out, false, nil
}

// Judge is the compaction setting's verdict over the rollup, with the
// first judged week. ok is false when nothing has been set.
func (w Weeks) Judge() (compactwatch.Verdict, time.Time, bool) {
	if w.SetAt.IsZero() {
		return compactwatch.Verdict{}, time.Time{}, false
	}
	from := judgedFrom(w.SetAt)
	var series []compactwatch.Week
	for _, f := range w.Weeks {
		if f.Prompts == 0 {
			// No band had enough instructions to judge the week's rejects.
			continue
		}
		series = append(series, compactwatch.Week{Start: f.Start, OversizedTokens: f.OversizedTokens, RejectRatePct: f.RejectRatePct})
	}
	return compactwatch.Judge(series, from, compactwatch.MinWeeks), from, true
}

// ReadWeeks reads the rollup in dir; nil when there is none.
func ReadWeeks(dir string) (*Weeks, error) {
	b, err := os.ReadFile(filepath.Join(dir, weeksFile)) //nolint:gosec // TokenOps' own cache directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var w Weeks
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, err
	}
	return &w, nil
}

func writeWeeks(dir string, w Weeks) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(w)
	if err != nil {
		return err
	}
	tmp, err := tempfile.Create(dir, weeksFile+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, weeksFile))
}
