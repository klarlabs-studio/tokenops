// Package effortweeks keeps the developer's own weekly series per model
// and effort, and judges each observed change of the level a model mostly
// runs at (ADR 0022). The weeks are a small stored rollup (ADR 0019): a
// finished week is computed once, from the transcripts and the turns'
// list prices, and again only when a change in the event log touches it or
// the rollup's version changes. The judgement itself is
// internal/contexts/learning/effortwatch.
package effortweeks

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

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/learning/effortwatch"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/internal/tempfile"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Version is the rollup's layout and computation version. A rollup of
// another version is rebuilt.
const Version = 1

// Kept is how many finished weeks the rollup holds: enough for MinWeeks
// either side of a change made up to two months ago.
const Kept = 16

const file = "effort-weeks.json"

// Rollup is the stored series.
type Rollup struct {
	Version int `json:"version"`
	// Position is where the event log stood when the weeks were computed.
	Position   sqlite.Position `json:"position"`
	ComputedAt time.Time       `json:"computed_at"`
	Weeks      []Week          `json:"weeks"`
}

// Week is one finished week (Monday to Monday, UTC), per client and model.
type Week struct {
	Start  time.Time   `json:"start"`
	Models []ModelWeek `json:"models,omitempty"`
}

// ModelWeek is one model's week in one client.
type ModelWeek struct {
	Client       string `json:"client,omitempty"`
	Model        string `json:"model"`
	Instructions int    `json:"instructions"`
	// Levels counts the instructions at each effort level.
	Levels map[string]int `json:"levels"`
	// USDPerInstruction is what one instruction cost at list prices,
	// zero when no cost was read.
	USDPerInstruction float64 `json:"usd_per_instruction"`
	FirstTryPct       float64 `json:"first_try_pct"`
	RejectedPct       float64 `json:"rejected_pct"`
	InterruptedPct    float64 `json:"interrupted_pct"`
}

// dominant is the level with the most instructions; ties go to the lower
// level name, so the result does not depend on map order.
func (m ModelWeek) dominant() string {
	best, n := "", 0
	for lvl, c := range m.Levels {
		if c > n || (c == n && lvl < best) {
			best, n = lvl, c
		}
	}
	return best
}

// Records reads the transcript entries over [since, until).
type Records func(ctx context.Context, since, until time.Time) ([]agentdx.Record, error)

// Turns reads the model turns recorded since, with their list prices.
type Turns func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error)

// Figures computes one week from scratch: the reference the rollup is
// checked against (ADR 0019, decision 5).
func Figures(ctx context.Context, records Records, turns Turns, start time.Time) (Week, error) {
	end := start.AddDate(0, 0, 7)
	recs, err := records(ctx, start, end)
	if err != nil {
		return Week{}, err
	}
	inWeek := recs[:0:0]
	for _, r := range recs {
		if !r.At.Before(start) && r.At.Before(end) {
			inWeek = append(inWeek, r)
		}
	}
	var cost agentdx.CostOf
	if turns != nil {
		ts, err := turns(ctx, start)
		if err != nil {
			return Week{}, err
		}
		by := map[string][]agentdx.TurnCost{}
		for _, t := range ts {
			if t.At.Before(end) {
				by[t.SessionID] = append(by[t.SessionID], agentdx.TurnCost{At: t.At, USD: t.APIEquivalentUSD})
			}
		}
		cost = agentdx.UnitCosts(by)
	}
	return weekOf(start, agentdx.ByEffortCosted(inWeek, cost)), nil
}

// weekOf pools each model's effort rows into one week.
func weekOf(start time.Time, rows []agentdx.EffortRow) Week {
	type key struct{ client, model string }
	type acc struct {
		n                         int
		usd                       float64
		firstTry, rejected, intrp float64
		levels                    map[string]int
	}
	by := map[key]*acc{}
	var order []key
	for _, r := range rows {
		k := key{r.Client, r.Model}
		a, ok := by[k]
		if !ok {
			a = &acc{levels: map[string]int{}}
			by[k] = a
			order = append(order, k)
		}
		n := float64(r.Instructions)
		a.n += r.Instructions
		a.usd += r.APIEquivalentUSD
		a.firstTry += r.FirstTryPct * n
		a.rejected += r.RejectedPct * n
		a.intrp += r.InterruptedPct * n
		a.levels[r.Effort] += r.Instructions
	}
	w := Week{Start: start.UTC()}
	for _, k := range order {
		a := by[k]
		if a.n == 0 {
			continue
		}
		n := float64(a.n)
		w.Models = append(w.Models, ModelWeek{
			Client: k.client, Model: k.model, Instructions: a.n, Levels: a.levels,
			USDPerInstruction: a.usd / n, FirstTryPct: a.firstTry / n, RejectedPct: a.rejected / n, InterruptedPct: a.intrp / n,
		})
	}
	return w
}

// Log is the event log the rollup follows.
type Log interface {
	Position(ctx context.Context) (sqlite.Position, error)
	ChangedTimes(ctx context.Context, t eventschema.EventType, from, to sqlite.Position) ([]time.Time, bool, error)
}

// weekStart is the Monday 00:00 UTC that starts t's week.
func weekStart(t time.Time) time.Time {
	t = t.UTC()
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

// Refresh brings the rollup in dir up to date at now and returns it: it
// computes the last Kept finished weeks it does not hold, and those a
// change in the log since the last refresh touched, and drops older ones.
func Refresh(ctx context.Context, log Log, figures func(ctx context.Context, start time.Time) (Week, error), dir string, now time.Time) (Rollup, error) {
	if log == nil || figures == nil {
		return Rollup{}, errors.New("effort weeks: no event log to follow")
	}
	pos, err := log.Position(ctx)
	if err != nil {
		return Rollup{}, err
	}
	held := map[time.Time]Week{}
	if old, _ := Read(dir); old != nil && old.Version == Version {
		for _, w := range old.Weeks {
			held[w.Start.UTC()] = w
		}
		dirty, err := touched(ctx, log, old.Position, pos, now)
		if err != nil {
			return Rollup{}, err
		}
		for w := range held {
			if dirty(w) {
				delete(held, w)
			}
		}
	}
	out := Rollup{Version: Version, Position: pos, ComputedAt: now.UTC()}
	cur := weekStart(now)
	for i := Kept; i >= 1; i-- {
		start := cur.AddDate(0, 0, -7*i)
		w, ok := held[start]
		if !ok {
			if w, err = figures(ctx, start); err != nil {
				return Rollup{}, fmt.Errorf("effort week %s: %w", start.Format(time.DateOnly), err)
			}
		}
		out.Weeks = append(out.Weeks, w)
	}
	return out, write(dir, out)
}

// touched reports, for each week start, whether a change in the log
// between from and to touched that week. Every week is touched when a
// change cannot be placed in time, or there is no position to compare
// with. A change past the change log's horizon (the retention prune, a
// reprice of old events) is not placed one by one, but it can touch only
// the weeks reaching back past that horizon (sqlite.OlderChangedBefore).
func touched(ctx context.Context, log Log, from, to sqlite.Position, now time.Time) (func(time.Time) bool, error) {
	every := func(time.Time) bool { return true }
	if from == (sqlite.Position{}) {
		return every, nil
	}
	if from == to {
		return func(time.Time) bool { return false }, nil
	}
	older, ok := sqlite.OlderChangedBefore(from, to, now)
	if !ok {
		return every, nil
	}
	times, attributable, err := log.ChangedTimes(ctx, eventschema.EventTypePrompt, from, to)
	if err != nil {
		return nil, err
	}
	if !attributable {
		return every, nil
	}
	placed := map[time.Time]bool{}
	for _, t := range times {
		placed[weekStart(t)] = true
	}
	return func(w time.Time) bool { return placed[w] || w.Before(older) }, nil
}

// OutcomeMargin is how far an outcome rate may worsen after a change, in
// percentage points, and still count as not worse: the margin the effort
// suggestion uses.
const OutcomeMargin = agentdx.EffortOutcomeMargin

// The judgement's types, aliased so a surface can render them without
// reaching into the domain.
type (
	// Verdict is the judgement over one model's series.
	Verdict = effortwatch.Verdict
	// Change is the week a model's usual level changed.
	Change = effortwatch.Change
	// Measure is one figure before and after the change.
	Measure = effortwatch.Measure
	// Interval is a weekly mean with its 95% interval.
	Interval = effortwatch.Interval
)

// Watch is one model's latest observed change of effort and its verdict.
type Watch struct {
	Client  string              `json:"client,omitempty"`
	Model   string              `json:"model"`
	Verdict effortwatch.Verdict `json:"verdict"`
	// PreventedUSDPerWeek is the realized saving a week: the fall in cost
	// per instruction times the weekly instructions after the change. An
	// estimate, zero unless Realized.
	PreventedUSDPerWeek float64 `json:"prevented_usd_per_week_estimate,omitempty"`
}

// Watches judges every model whose dominant level changed within the
// rollup, in client and model order.
func (r Rollup) Watches() []Watch {
	type key struct{ client, model string }
	series := map[key][]effortwatch.Week{}
	weeks := slices.Clone(r.Weeks)
	slices.SortFunc(weeks, func(a, b Week) int { return a.Start.Compare(b.Start) })
	for _, w := range weeks {
		for _, m := range w.Models {
			k := key{m.Client, m.Model}
			series[k] = append(series[k], effortwatch.Week{
				Start: w.Start, Instructions: m.Instructions, Dominant: m.dominant(),
				USDPerInstruction: m.USDPerInstruction, FirstTryPct: m.FirstTryPct,
				RejectedPct: m.RejectedPct, InterruptedPct: m.InterruptedPct,
			})
		}
	}
	var out []Watch
	for k, s := range series {
		c, ok := effortwatch.LatestChange(s)
		if !ok {
			continue
		}
		v := effortwatch.Judge(s, c, OutcomeMargin)
		w := Watch{Client: k.client, Model: k.model, Verdict: v}
		if v.Realized {
			w.PreventedUSDPerWeek = -v.Cost.Change * v.WeeklyInstructionsAfter
		}
		out = append(out, w)
	}
	slices.SortFunc(out, func(a, b Watch) int {
		if a.Client != b.Client {
			return compare(a.Client, b.Client)
		}
		return compare(a.Model, b.Model)
	})
	return out
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// DefaultDir is ~/.tokenops/cache, beside the other derived rollups.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".tokenops", "cache")
	}
	return filepath.Join(home, ".tokenops", "cache")
}

// Stored is the judged changes in the default directory's rollup; nil when
// there is none or it cannot be read.
func Stored() []Watch {
	r, err := Read(DefaultDir())
	if err != nil || r == nil {
		return nil
	}
	return r.Watches()
}

// Read reads the rollup in dir; nil when there is none.
func Read(dir string) (*Rollup, error) {
	b, err := os.ReadFile(filepath.Join(dir, file)) //nolint:gosec // TokenOps' own cache directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Rollup
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func write(dir string, r Rollup) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := tempfile.Create(dir, file+".*")
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
	return os.Rename(tmp.Name(), filepath.Join(dir, file))
}
