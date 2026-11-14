package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
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
		events:   func(context.Context, time.Time) (string, bool) { return "events", true },
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

// A restart does not bring the next computation forward: it is due a day
// after the last, unless the settings changed or there is no mark.
func TestObservedFirstWait(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		mark *observedMark
		want time.Duration
	}{
		{"no mark", nil, observedDelay},
		{"computed an hour ago", &observedMark{At: now.Add(-time.Hour), Settings: "s"}, 23 * time.Hour},
		{"computed just before the delay runs out", &observedMark{At: now.Add(-observedEvery + time.Minute), Settings: "s"}, observedDelay},
		{"computed two days ago", &observedMark{At: now.Add(-48 * time.Hour), Settings: "s"}, observedDelay},
		{"settings changed", &observedMark{At: now.Add(-time.Hour), Settings: "old"}, observedDelay},
	}
	for _, c := range cases {
		if got := observedFirstWait(c.mark, "s", observedDelay, observedEvery, now); got != c.want {
			t.Errorf("%s: first wait %s, want %s", c.name, got, c.want)
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

// The overlay kept while its inputs stand is the one the full
// computation gives: through inserts, updates and deletes across the 90
// days — older than the change log's horizon too — each pass either
// computes again or keeps a result equal to computing again.
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
	job := observedJob{options: options, settings: func() string { return "settings" }, events: observedEvents(store),
		dir: filepath.Join(home, ".tokenops", "cache")}
	exec := func(stmts ...string) {
		t.Helper()
		for _, q := range stmts {
			if _, err := store.DB().ExecContext(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
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
		// computes is whether the pass must compute again.
		computes bool
	}{
		{"nothing changed", func() {}, false},
		{"a reading repeated", func() {
			r := observedReading("repeat", start.Add(9*7*24*time.Hour), "40")
			r.Timestamp = r.Timestamp.Add(time.Hour)
			r.Attributes[eventschema.AttrRepeat] = eventschema.AttrRepeatValue
			appendEvents(r)
		}, false},
		{"a recent turn", func() { appendEvents(observedTurn("t-new", start.Add(9*7*24*time.Hour+2*time.Hour), 30000)) }, true},
		{"a recent turn updated", func() {
			exec(`UPDATE events SET input_tokens = 500, payload = json_set(payload, '$.input_tokens', 500) WHERE id = 't-new'`)
		}, true},
		{"old turns updated, past the change log's horizon", func() {
			exec(fmt.Sprintf(`UPDATE events SET input_tokens = input_tokens * 2,
				payload = json_set(payload, '$.input_tokens', json_extract(payload, '$.input_tokens') * 2)
				WHERE source = 'claude-code-jsonl' AND timestamp_ns < %d`, old))
		}, true},
		{"an old reading deleted", func() { exec(`DELETE FROM events WHERE id = 'r1'`) }, true},
		{"an old reading stored late", func() { appendEvents(observedReading("r1-late", start.Add(7*24*time.Hour), "40")) }, true},
		{"a recent turn deleted", func() { exec(`DELETE FROM events WHERE id = 't-new'`) }, true},
		{"nothing changed again", func() {}, false},
	}
	at := now
	mark := keepObserved(ctx, job, nil, at, quietLogger())
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
		at = at.Add(time.Minute)
		next := keepObserved(ctx, job, mark, at, quietLogger())
		full := checkup.ProviderDrifts(ctx, options(at))
		computed := next != mark
		if computed != st.computes {
			t.Fatalf("%s: computed = %v, want %v", st.name, computed, st.computes)
		}
		if computed {
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
	if next := keepObserved(ctx, job, readObservedMark(job.dir), at.Add(time.Hour), quietLogger()); !next.At.Equal(mark.At) {
		t.Fatal("a restart with nothing moved computed again")
	}
	// Past its max age the result is computed again, nothing moved or not.
	if next := keepObserved(ctx, job, mark, mark.At.Add(observedMaxAge), quietLogger()); next.At.Equal(mark.At) {
		t.Fatal("a result past its max age was kept")
	}
	// So it is when the settings change.
	job.settings = func() string { return "other" }
	if next := keepObserved(ctx, job, mark, at.Add(time.Hour), quietLogger()); next.At.Equal(mark.At) {
		t.Fatal("a result under other settings was kept")
	}
}

// BenchmarkObservedPass is one pass over 90 days of a busy store: the
// full computation, against the check that finds nothing moved.
func BenchmarkObservedPass(b *testing.B) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
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
			envs = append(envs, observedReading(fmt.Sprintf("r%d", i), at.Add(-3*24*time.Hour), "40"))
			continue
		}
		envs = append(envs, observedTurn(fmt.Sprintf("t%d", i), at, int64(1000+i%500)))
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		b.Fatal(err)
	}
	options := func(at time.Time) checkup.Options {
		return checkup.Options{Now: at, Events: store, Plans: map[string]string{"anthropic": "claude-max"},
			Price: func(p *eventschema.PromptEvent, _ time.Time) (float64, error) {
				return float64(p.InputTokens) / 100, nil
			}}
	}
	b.Run("full computation", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = checkup.ProviderDrifts(ctx, options(now))
		}
	})
	b.Run("inputs unchanged", func(b *testing.B) {
		job := observedJob{options: options, settings: func() string { return "s" }, events: observedEvents(store), dir: b.TempDir()}
		ev, _ := job.events(ctx, now)
		mark := &observedMark{Version: observedVersion, At: now, Settings: "s", Events: ev}
		b.ReportAllocs()
		for b.Loop() {
			if keepObserved(ctx, job, mark, now.Add(time.Minute), quietLogger()) != mark {
				b.Fatal("computed again")
			}
		}
	})
}
