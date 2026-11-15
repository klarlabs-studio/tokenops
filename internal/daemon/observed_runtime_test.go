package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/checkup"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type noEvents struct{}

func (noEvents) ReadEvents(context.Context, eventschema.EventType, time.Time) ([]*eventschema.Envelope, error) {
	return nil, nil
}

func sandboxObserved(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, home)
	}
	return home
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// The runtime keeps the overlay once after its delay, under the home it
// is given, marks the inputs it reflects, and stops with its context.
func TestRunObservedKeepsTheOverlay(t *testing.T) {
	home := sandboxObserved(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	job := observedJob{
		options: func(now time.Time) checkup.Options {
			return checkup.Options{Now: now, Events: noEvents{}, Plans: map[string]string{"anthropic": "claude-max"},
				Price: func(*eventschema.PromptEvent, time.Time) (float64, error) { return 1, nil }}
		},
		settings: func() string { return "settings" },
		dir:      filepath.Join(home, ".tokenops", "cache"),
	}
	go func() {
		runObserved(ctx, job, 0, time.Hour, quietLogger())
		close(done)
	}()
	path := filepath.Join(home, ".tokenops", "observed-limits.json")
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, kept := os.Stat(path)
		if kept == nil && readObservedMark(job.dir) != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the overlay was not kept and marked")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the runtime did not stop with its context")
	}
}

// A result is kept while its inputs stand: the position unchanged, the
// digest checked within a day, the window read within its max age.
func TestFresh(t *testing.T) {
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	mark := observedMark{At: at, Built: at, Checked: at}
	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"just kept", at, true},
		{"an hour later", at.Add(time.Hour), true},
		{"a day later: the window slides", at.Add(observedEvery), false},
		{"a week later: rebuilt", at.Add(observedMaxAge), false},
		{"clock went back", at.Add(-time.Minute), false},
	}
	for _, c := range cases {
		if got := fresh(&mark, c.now); got != c.want {
			t.Errorf("%s: fresh = %v, want %v", c.name, got, c.want)
		}
	}
}

// driftStore is a store holding ten weekly windows of Sonnet work from
// start, five on Pro and five on Max, each read at 40%, and the price
// that values each request at a cent per input token.
func driftStore(t *testing.T, start time.Time) (*sqlite.Store, plans.Pricer) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	envs := make([]*eventschema.Envelope, 0, 20)
	for i := range 10 {
		open := start.Add(time.Duration(i) * 7 * 24 * time.Hour)
		envs = append(envs, observedReading(fmt.Sprintf("r%d", i), open, "40"))
		tokens := int64(4000)
		if i >= 5 {
			tokens = 10000 // 2.5x Pro's per percent; the catalog says 5x
		}
		envs = append(envs, observedTurn(fmt.Sprintf("t%d", i), open.Add(time.Hour), tokens))
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}
	price := func(p *eventschema.PromptEvent, _ time.Time) (float64, error) {
		return float64(p.InputTokens) / 100, nil
	}
	return store, price
}

func observedReading(id string, open time.Time, pct string) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Source: "claude-code-statusline", Timestamp: open.Add(3 * 24 * time.Hour),
		Attributes: map[string]string{"granularity": "quota_snapshot", "seven_day_kind": "weekly_all",
			"seven_day_used_pct": pct, "seven_day_reset_at": open.Add(7 * 24 * time.Hour).Format(time.RFC3339)},
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
	}
}

func observedTurn(id string, at time.Time, tokens int64) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Source: "claude-code-jsonl", Timestamp: at,
		Attributes: map[string]string{"granularity": "assistant_turn"},
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-5",
			InputTokens: tokens, OutputTokens: 1, CostSource: eventschema.CostSourcePlanIncluded},
	}
}

// The overlay kept by the day inputs is the one the full computation gives
// through inserts, late rows, updates and deletes, the ones inside the
// change log's horizon and the ones past it. Each step says whether the
// pass must compute again; whatever it computes is compared with the full
// computation at the same instant.
func TestKeepObservedMatchesTheFullComputation(t *testing.T) {
	home := sandboxObserved(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	start := now.AddDate(0, 0, -80)
	store, price := driftStore(t, start)
	history := plans.History{
		{Provider: "anthropic", Plan: "claude-pro", From: start.AddDate(0, 0, -30)},
		{Provider: "anthropic", Plan: "claude-max", From: start.Add(5 * 7 * 24 * time.Hour)},
	}
	options := func(at time.Time) checkup.Options {
		return checkup.Options{Now: at, Events: store, Plans: map[string]string{"anthropic": "claude-max"}, PlanHistory: history, Price: price}
	}
	job := observedJob{options: options, settings: func() string { return "settings" }, store: store,
		hours: newHours(home), dir: filepath.Join(home, ".tokenops", "cache")}
	exec := func(q string) {
		t.Helper()
		if _, err := store.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	appendEvents := func(envs ...*eventschema.Envelope) {
		t.Helper()
		if err := store.AppendBatch(ctx, envs); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-sqlite.ChangeLogHorizon - 10*24*time.Hour).UnixNano()
	steps := []struct {
		name string
		do   func()
		// advance is how far the clock moves: a change past the horizon
		// is seen by the digest, which is checked once a day.
		advance time.Duration
		// computes is whether the pass must compute again.
		computes bool
	}{
		{"nothing changed", func() {}, time.Minute, false},
		{"a reading repeated", func() {
			r := observedReading("repeat", start.Add(9*7*24*time.Hour), "40")
			r.Timestamp = r.Timestamp.Add(time.Hour)
			r.Attributes[eventschema.AttrRepeat] = eventschema.AttrRepeatValue
			appendEvents(r)
		}, time.Minute, false},
		{"a recent turn", func() { appendEvents(observedTurn("t-new", start.Add(9*7*24*time.Hour+2*time.Hour), 30000)) },
			time.Minute, true},
		{"a recent turn updated", func() {
			exec(`UPDATE events SET input_tokens = 500, payload = json_set(payload, '$.input_tokens', 500) WHERE id = 't-new'`)
		}, time.Minute, true},
		{"old turns updated, past the change log's horizon", func() {
			exec(fmt.Sprintf(`UPDATE events SET input_tokens = input_tokens * 2,
				payload = json_set(payload, '$.input_tokens', json_extract(payload, '$.input_tokens') * 2)
				WHERE source = 'claude-code-jsonl' AND timestamp_ns < %d`, old))
		}, observedEvery, true},
		{"an old reading deleted, past the horizon", func() { exec(`DELETE FROM events WHERE id = 'r1'`) },
			observedEvery, true},
		{"an old reading stored late", func() { appendEvents(observedReading("r1-late", start.Add(7*24*time.Hour), "40")) },
			time.Minute, true},
		{"a recent turn deleted", func() { exec(`DELETE FROM events WHERE id = 't-new'`) }, time.Minute, true},
		{"nothing changed again", func() {}, time.Minute, false},
	}
	at := now
	mark, _ := keepObserved(ctx, job, nil, at, quietLogger())
	if mark == nil {
		t.Fatal("the first pass kept no mark")
	}
	kept := checkup.ProviderDrifts(ctx, options(at))
	if len(kept["anthropic"]) == 0 {
		t.Fatal("the fixture drifts nothing")
	}
	seen := map[string]bool{}
	for _, st := range steps {
		st.do()
		at = at.Add(st.advance)
		next, drifts := keepObserved(ctx, job, mark, at, quietLogger())
		full := checkup.ProviderDrifts(ctx, options(at))
		computed := next != mark
		if computed != st.computes {
			t.Fatalf("%s: computed = %v, want %v", st.name, computed, st.computes)
		}
		if computed {
			if !reflect.DeepEqual(drifts, full) {
				t.Fatalf("%s: computed %+v, the full computation gives %+v", st.name, drifts, full)
			}
			kept = full
		} else if !reflect.DeepEqual(kept, full) {
			t.Fatalf("%s: kept %+v, the full computation gives %+v", st.name, kept, full)
		}
		seen[fmt.Sprint(full)] = true
		mark = next
	}
	if len(seen) < 3 {
		t.Fatalf("the steps moved the drifts only %d ways: the test checks little", len(seen))
	}
	// A restart reads the mark back and keeps the result.
	if got := readObservedMark(job.dir); got == nil || !got.At.Equal(mark.At) || got.Events != mark.Events || got.Settings != mark.Settings {
		t.Fatalf("mark read back = %+v, want %+v", got, mark)
	}
	if next, _ := keepObserved(ctx, job, readObservedMark(job.dir), at.Add(time.Hour), quietLogger()); !next.At.Equal(mark.At) {
		t.Fatal("a restart with nothing moved computed again")
	}
	// A restart with an empty cache reads the hours back from disk: a
	// change after it is computed from them, and agrees with the full read.
	restarted := job
	restarted.hours = newHours(home)
	appendEvents(observedTurn("t-restart", now.Add(-30*time.Minute), 9000))
	at = at.Add(time.Minute)
	next, drifts := keepObserved(ctx, restarted, mark, at, quietLogger())
	if next == mark || !reflect.DeepEqual(drifts, checkup.ProviderDrifts(ctx, options(at))) {
		t.Fatal("a restart's pass after a change does not match the full computation")
	}
	mark = next
	// Past its max age the window is read again, nothing moved or not.
	if next, _ := keepObserved(ctx, job, mark, mark.Built.Add(observedMaxAge), quietLogger()); next.At.Equal(mark.At) {
		t.Fatal("a result past its max age was kept")
	}
	// So it is when the settings change.
	job.settings = func() string { return "other" }
	if next, _ := keepObserved(ctx, job, mark, at.Add(time.Hour), quietLogger()); next.At.Equal(mark.At) {
		t.Fatal("a result under other settings was kept")
	}
}

// BenchmarkObservedPass is the cost of keeping the overlay when an hour of
// events arrived on a 90-day store: before, the full read the pass made
// each time its position moved; after, the pass that reads the days the
// change named. Each sub-benchmark has its own store; setup and the
// hour's inserts are not timed.
func BenchmarkObservedPass(b *testing.B) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	options := func(store *sqlite.Store) func(time.Time) checkup.Options {
		return func(at time.Time) checkup.Options {
			return checkup.Options{Now: at, Events: store, Plans: map[string]string{"anthropic": "claude-max"},
				Price: func(p *eventschema.PromptEvent, _ time.Time) (float64, error) {
					return float64(p.InputTokens) / 100, nil
				}}
		}
	}
	// newStore is a 90-day store of about 50k events, a busy machine's.
	newStore := func(b *testing.B) *sqlite.Store {
		store, err := sqlite.Open(ctx, filepath.Join(b.TempDir(), "events.db"), sqlite.Options{})
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { _ = store.Close() })
		const n = 50_000
		envs := make([]*eventschema.Envelope, 0, n)
		for i := range n {
			at := now.Add(-time.Duration(i) * (90 * 24 * time.Hour / n))
			if i%40 == 0 {
				envs = append(envs, weeklyReading(fmt.Sprintf("r%d", i), at, "40"))
				continue
			}
			envs = append(envs, observedTurn(fmt.Sprintf("t%d", i), at, int64(1000+i%500)))
		}
		if err := store.AppendBatch(ctx, envs); err != nil {
			b.Fatal(err)
		}
		return store
	}
	// hour appends the hour that ends at at: about a busy hour's turns and
	// one window reading.
	hour := func(b *testing.B, store *sqlite.Store, at time.Time) {
		batch := make([]*eventschema.Envelope, 0, 26)
		for j := range 25 {
			batch = append(batch, observedTurn(fmt.Sprintf("h%d-%d", at.Unix(), j), at.Add(-time.Duration(j)*2*time.Minute), 1200))
		}
		batch = append(batch, weeklyReading(fmt.Sprintf("hr%d", at.Unix()), at.Add(-time.Minute), "41"))
		if err := store.AppendBatch(ctx, batch); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("before: full read after the hour", func(b *testing.B) {
		store := newStore(b)
		b.ReportAllocs()
		for i := range b.N {
			at := now.Add(time.Duration(i+1) * time.Hour)
			b.StopTimer()
			hour(b, store, at)
			b.StartTimer()
			_ = checkup.ProviderDrifts(ctx, options(store)(at))
		}
	})
	b.Run("after: days changed by the hour", func(b *testing.B) {
		store := newStore(b)
		job := observedJob{options: options(store), settings: func() string { return "s" }, store: store,
			hours: newHours(b.TempDir()), dir: b.TempDir()}
		mark, _ := keepObserved(ctx, job, nil, now, quietLogger())
		b.ReportAllocs()
		for i := range b.N {
			at := now.Add(time.Duration(i+1) * time.Hour)
			b.StopTimer()
			hour(b, store, at)
			b.StartTimer()
			mark, _ = keepObserved(ctx, job, mark, at, quietLogger())
		}
	})
}

// The pass reads only the hours a change names, and every hour when the
// change cannot be placed in time (a recent delete, whose time is gone).
func TestDirtyHoursFollowTheChanges(t *testing.T) {
	home := sandboxObserved(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	start := now.AddDate(0, 0, -80)
	store, price := driftStore(t, start)
	options := func(at time.Time) checkup.Options {
		return checkup.Options{Now: at, Events: store, Plans: map[string]string{"anthropic": "claude-max"}, Price: price}
	}
	job := observedJob{options: options, settings: func() string { return "s" }, store: store,
		hours: newHours(home), dir: filepath.Join(home, ".tokenops", "cache")}
	exec := func(q string) {
		t.Helper()
		if _, err := store.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	cases := []struct {
		name string
		do   func()
		want []time.Time // the hours read; nil means every hour
	}{
		{"nothing changed", func() {}, []time.Time{}},
		{"a turn inserted an hour ago", func() {
			_ = store.AppendBatch(ctx, []*eventschema.Envelope{observedTurn("d1", now.Add(-time.Hour), 100)})
		}, []time.Time{hourAt(now.Add(-time.Hour))}},
		{"a late reading stored into an old hour", func() {
			_ = store.AppendBatch(ctx, []*eventschema.Envelope{observedReading("late", start.Add(20*24*time.Hour), "41")})
		}, []time.Time{hourAt(start.Add(23 * 24 * time.Hour))}},
		{"a recent turn updated", func() {
			exec(`UPDATE events SET input_tokens = 7 WHERE id = 'd1'`)
		}, []time.Time{hourAt(now.Add(-time.Hour))}},
		{"a recent turn deleted", func() { exec(`DELETE FROM events WHERE id = 'd1'`) }, nil},
	}
	pos, err := job.position(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := job.hours.Drifts(ctx, job.eventLog(), job.options(now), "s", pos, true); err != nil {
		t.Fatal(err)
	}
	allHours := int(hourAt(now).Sub(hourAt(now.AddDate(0, 0, -checkup.DriftDays)))/time.Hour) + 1
	for _, c := range cases {
		c.do()
		to, err := job.position(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := job.hours.Drifts(ctx, job.eventLog(), job.options(now), "s", to, false); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := sortedHours(job.hours.Read())
		if c.want == nil {
			if len(got) != allHours {
				t.Fatalf("%s: %d hours read, want all %d", c.name, len(got), allHours)
			}
		} else if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s: hours read %v, want %v", c.name, got, c.want)
		}
	}
}

func sortedHours(m map[time.Time]bool) []time.Time {
	out := make([]time.Time, 0, len(m))
	for h := range m {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// weeklyReading is a reading of the weekly window that resets at the end
// of its week, as the vendor's meter reports it: every reading of a week
// shares one window.
func weeklyReading(id string, at time.Time, pct string) *eventschema.Envelope {
	const week = 7 * 24 * time.Hour
	reset := at.Truncate(week).Add(week)
	r := observedReading(id, reset.Add(-week), pct)
	r.Timestamp = at
	r.Attributes["seven_day_reset_at"] = reset.Format(time.RFC3339)
	return r
}

// newHours is an empty hour cache under home's cache directory.
func newHours(home string) *checkup.DriftHours {
	return &checkup.DriftHours{Dir: filepath.Join(home, ".tokenops", "cache", observedDaysDir), Version: observedVersion}
}

// hourAt is the UTC hour an instant falls in.
func hourAt(t time.Time) time.Time { return t.UTC().Truncate(time.Hour) }
