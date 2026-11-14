package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// seededGlanceStore is an event store with a week of Claude Code and
// Codex work and a fresh window reading from each, as of now.
func seededGlanceStore(t testing.TB, now time.Time, prompts int) *sqlite.Store {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	envs := make([]*eventschema.Envelope, 0, prompts+2)
	for i := range prompts {
		at := now.Add(-time.Duration(i+1) * 7 * 24 * time.Hour / time.Duration(prompts+1))
		src, provider, model := "claude-code-jsonl", eventschema.ProviderAnthropic, "claude-opus-5"
		if i%2 == 1 {
			src, provider, model = "codex-jsonl", eventschema.ProviderOpenAI, "gpt-5.5"
		}
		envs = append(envs, &eventschema.Envelope{
			ID: fmt.Sprintf("p%d", i), SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: at, Source: src,
			Payload: &eventschema.PromptEvent{Provider: provider, RequestModel: model, InputTokens: 4000, OutputTokens: 800,
				CachedInputTokens: 2000, TotalTokens: 4800},
		})
	}
	envs = append(envs,
		&eventschema.Envelope{ID: "claude-reading", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: now.Add(-time.Minute), Source: "claude-code-statusline",
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
			Attributes: map[string]string{
				"five_hour_used_pct": "41.00", "five_hour_reset_at": now.Add(3 * time.Hour).Format(time.RFC3339),
				"seven_day_used_pct": "63.00", "seven_day_reset_at": now.Add(50 * time.Hour).Format(time.RFC3339),
				"granularity": "quota_snapshot",
			}},
		&eventschema.Envelope{ID: "codex-reading", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: now.Add(-2 * time.Minute), Source: "codex-jsonl",
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI},
			Attributes: map[string]string{
				"primary_used_pct": "22", "primary_window_minutes": "300",
				"primary_resets_at":  fmt.Sprint(now.Add(4 * time.Hour).Unix()),
				"secondary_used_pct": "35", "secondary_window_minutes": "10080",
				"secondary_resets_at": fmt.Sprint(now.Add(80 * time.Hour).Unix()),
			}},
	)
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}
	return store
}

// glanceServer serves the glance from store as the daemon does; follow
// has its figures follow the store's position.
func glanceServer(store *sqlite.Store, follow bool) *Server {
	cfg := &config.Config{Plans: map[string]string{"anthropic": "claude-ultra", "openai": "gpt-pro"}}
	events := sqlite.NewEventCache(store, 0, 0)
	opts := []Option{WithPlans(func() headroom.Deps { return headroom.LocalDeps(cfg, events, nil) })}
	if follow {
		inputs := func(ctx context.Context) (string, bool) {
			p, err := store.Position(ctx)
			return fmt.Sprint(p), err == nil
		}
		opts = append(opts, WithGlanceFollows(inputs, config.DefaultDerivedMaxAge))
	}
	return New("127.0.0.1:0", opts...)
}

// timeOnly blanks what a glance shows that moves with the clock alone,
// and so differs between the figures computed at one instant and answered
// at a later one with no event between: when they were computed for, a
// window's "resets in", pace and days of headroom, and the periods plan
// use and the plan advice cover, which end at the instant they were
// computed for, so the advice's replayed lockouts (blocked minutes) run
// to it too.
func timeOnly(t *testing.T, p *headroom.GlancePayload) map[string]any {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var blank func(any)
	blank = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, x := range v {
				switch k {
				case "computed_at", "resets_in", "window_resets_in", "pace", "since", "until", "headroom_days", "blocked_minutes":
					v[k] = "time-only"
				default:
					blank(x)
				}
			}
		case []any:
			for _, x := range v {
				blank(x)
			}
		}
	}
	blank(m)
	return m
}

// A glance answered after a quiet interval is the glance a fresh
// computation gives at that instant, but for what moves with the clock
// alone (timeOnly).
func TestGlanceAfterAQuietIntervalIsAFreshOne(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	now := time.Now().UTC().Truncate(time.Second)
	store := seededGlanceStore(t, now, 200)
	ctx := context.Background()
	following := glanceServer(store, true)
	if _, err := following.computeView(ctx, now); err != nil {
		t.Fatal(err)
	}
	later := now.Add(2 * time.Minute)
	kept, err := following.computeView(ctx, later)
	if err != nil {
		t.Fatal(err)
	}
	if !kept.Glance.ComputedAt.Equal(now) {
		t.Fatalf("computed_at %s after a quiet interval, want the figures of %s kept", kept.Glance.ComputedAt, now)
	}
	fresh, err := glanceServer(store, false).computeView(ctx, later)
	if err != nil {
		t.Fatal(err)
	}
	got, want := timeOnly(t, kept.Glance), timeOnly(t, fresh.Glance)
	gb, _ := json.MarshalIndent(got, "", " ")
	wb, _ := json.MarshalIndent(want, "", " ")
	if string(gb) != string(wb) {
		t.Errorf("kept glance differs from a fresh one beyond the time-only fields:\nkept:  %s\nfresh: %s", gb, wb)
	}
	if len(kept.Glance.PlanHeadroom.Reports) != 2 {
		t.Fatalf("reports = %+v, want both plans", kept.Glance.PlanHeadroom)
	}
}

// BenchmarkGlanceView is one glance computation on a week of events:
// kept answers the figures it has (nothing moved), recompute reads the
// events for them again (each run moves the inputs).
func BenchmarkGlanceView(b *testing.B) {
	b.Setenv("HOME", b.TempDir())
	b.Setenv("XDG_CONFIG_HOME", b.TempDir())
	now := time.Now().UTC()
	store := seededGlanceStore(b, now, 20000)
	ctx := context.Background()
	for _, kept := range []bool{true, false} {
		name := "recompute"
		if kept {
			name = "kept"
		}
		b.Run(name, func(b *testing.B) {
			s := glanceServer(store, true)
			if _, err := s.computeView(ctx, now); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				if !kept {
					s.glanceFollow.in = fmt.Sprint("moved", i)
				}
				if _, err := s.computeView(ctx, now.Add(time.Second)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
