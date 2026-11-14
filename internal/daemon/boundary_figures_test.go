package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readingEvents answers one Claude window reading and counts its reads.
type readingEvents struct {
	reading *eventschema.Envelope
	reads   *atomic.Int32
}

func (r readingEvents) ReadEvents(context.Context, eventschema.EventType, time.Time) ([]*eventschema.Envelope, error) {
	r.reads.Add(1)
	return []*eventschema.Envelope{r.reading}, nil
}

func (r readingEvents) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return map[string]int64{}, nil
}

// The boundary figures read plan headroom from the events again only when
// the inputs moved, a reset they hold passed or their max age did; the
// file is written on every measurement all the same, observed then, and
// says when its figures were computed for.
func TestBoundaryFiguresFollowTheInputs(t *testing.T) {
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	reset := at.Add(3 * time.Minute)
	var reads atomic.Int32
	reading := &eventschema.Envelope{Source: "claude-code-statusline", Timestamp: at.Add(-time.Minute),
		Type: eventschema.EventTypePrompt, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
		Attributes: map[string]string{
			"five_hour_used_pct": "40.00", "five_hour_reset_at": reset.Format(time.RFC3339),
			"seven_day_used_pct": "60.00", "seven_day_reset_at": at.Add(50 * time.Hour).Format(time.RFC3339),
			"granularity": "quota_snapshot",
		}}
	cfg := &config.Config{Plans: map[string]string{"anthropic": "claude-max"}}
	rules, err := domain.Parse([]domain.RuleSpec{{Name: "plan-floor", Action: "continue", Require: "deny",
		When: map[string]string{"plan_left_pct": "< 5", "window": "week"}}})
	if err != nil {
		t.Fatal(err)
	}
	pos := "1"
	dir := boundaryledger.Dir(t.TempDir())
	b := &boundaryFigures{
		rules:  func() []domain.Rule { return rules },
		glance: func() headroom.Deps { return headroom.Deps{Config: cfg, Reader: readingEvents{reading, &reads}} },
		dir:    dir,
		inputs: func(context.Context) (string, bool) { return pos, true },
		maxAge: 5 * time.Minute,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	steps := []struct {
		name     string
		now      time.Time
		pos      string
		computes bool
		computed time.Time
	}{
		{name: "first", now: at, pos: "1", computes: true, computed: at},
		{name: "nothing moved", now: at.Add(time.Minute), pos: "1", computed: at},
		{name: "an event arrived", now: at.Add(2 * time.Minute), pos: "2", computes: true, computed: at.Add(2 * time.Minute)},
		{name: "the 5h window reset", now: reset, pos: "2", computes: true, computed: reset},
		{name: "quiet", now: reset.Add(4 * time.Minute), pos: "2", computed: reset},
		{name: "past the max age", now: reset.Add(5 * time.Minute), pos: "2", computes: true, computed: reset.Add(5 * time.Minute)},
	}
	for _, st := range steps {
		pos = st.pos
		before := reads.Load()
		b.measure(context.Background(), st.now)
		if read := reads.Load() > before; read != st.computes {
			t.Errorf("%s: read the events %v, want %v", st.name, read, st.computes)
		}
		fig, ok, err := dir.GetFigures()
		if err != nil || !ok {
			t.Fatalf("%s: figures %v %v", st.name, ok, err)
		}
		if !fig.ObservedAt.Equal(st.now) || !fig.ComputedAt.Equal(st.computed) {
			t.Errorf("%s: observed %s computed %s, want %s and %s", st.name, fig.ObservedAt, fig.ComputedAt, st.now, st.computed)
		}
		if left, ok := fig.PlanLeftPct["anthropic"]["week"]; !ok || left != 40 {
			t.Errorf("%s: week left %v (%v), want 40", st.name, left, ok)
		}
	}
}

// BenchmarkBoundaryFigures is one measurement on a week of events: kept
// finds nothing moved, recompute reads plan headroom again each time.
func BenchmarkBoundaryFigures(b *testing.B) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(b.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	const prompts = 20000
	envs := make([]*eventschema.Envelope, 0, prompts)
	for i := range prompts {
		envs = append(envs, &eventschema.Envelope{
			ID: fmt.Sprintf("p%d", i), SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: now.Add(-time.Duration(i+1) * 7 * 24 * time.Hour / prompts), Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-5",
				InputTokens: 4000, OutputTokens: 800, TotalTokens: 4800},
		})
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		b.Fatal(err)
	}
	cfg := config.Config{Plans: map[string]string{"anthropic": "claude-max"}}
	rules, err := domain.Parse([]domain.RuleSpec{{Name: "plan-floor", Action: "continue", Require: "deny",
		When: map[string]string{"plan_left_pct": "< 5", "window": "week"}}})
	if err != nil {
		b.Fatal(err)
	}
	for _, kept := range []bool{true, false} {
		name := "recompute"
		if kept {
			name = "kept"
		}
		b.Run(name, func(b *testing.B) {
			var moves atomic.Int64
			f := &boundaryFigures{
				rules:  func() []domain.Rule { return rules },
				glance: plansDeps(cfg, newGlanceEvents(store), nil),
				dir:    boundaryledger.Dir(b.TempDir()),
				inputs: func(context.Context) (string, bool) {
					if kept {
						return "still", true
					}
					return fmt.Sprint(moves.Add(1)), true
				},
				maxAge: time.Hour,
				logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			f.measure(ctx, now)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				f.measure(ctx, now.Add(time.Second))
			}
		})
	}
}
