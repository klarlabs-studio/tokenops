package config

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeCounter is a hand-rolled SourceCounter so the stale-ingestion
// check is testable without a real event store. It records the window
// it was asked for so tests can assert the lookback is respected.
type fakeCounter struct {
	counts    map[string]int64
	err       error
	gotSince  time.Time
	gotUntil  time.Time
	callCount int
}

func (f *fakeCounter) CountBySource(_ context.Context, since, until time.Time) (map[string]int64, error) {
	f.callCount++
	f.gotSince, f.gotUntil = since, until
	if f.err != nil {
		return nil, f.err
	}
	return f.counts, nil
}

// VendorUsageSources is the single source of truth for the enabled-
// source↔tag mapping. This pins the exact tags so a rename in config
// that diverges from what the pollers stamp is caught here.
func TestVendorUsageSourcesTags(t *testing.T) {
	cfg := Default()
	got := cfg.VendorUsageSources()
	want := []struct{ name, tag string }{
		{"claude_code_jsonl", "claude-code-jsonl"},
		{"codex_jsonl", "codex-jsonl"},
		{"opencode", "opencode"},
		{"claude_code_stats_cache (deprecated)", "claude-code-stats-cache"},
		{"vendor_usage_anthropic", "vendor-usage-anthropic"},
		{"github_copilot", "github-copilot"},
		{"cursor_web", "cursor-web"},
		{"anthropic_cookie", "anthropic-cookie"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d sources, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Name != w.name || got[i].SourceTag != w.tag {
			t.Errorf("source[%d] = {%q, %q}, want {%q, %q}", i, got[i].Name, got[i].SourceTag, w.name, w.tag)
		}
	}
}

func TestEnabledVendorUsageSources(t *testing.T) {
	cfg := Default()
	if got := cfg.EnabledVendorUsageSources(); len(got) != 0 {
		t.Fatalf("fresh config should have no enabled sources; got %v", got)
	}
	cfg.VendorUsage.ClaudeCodeJSONL.Enabled = true
	cfg.VendorUsage.OpenCode.Enabled = true
	got := cfg.EnabledVendorUsageSources()
	if len(got) != 2 {
		t.Fatalf("want 2 enabled sources; got %d (%v)", len(got), got)
	}
	// Order must follow VendorUsageSources().
	if got[0].SourceTag != "claude-code-jsonl" || got[1].SourceTag != "opencode" {
		t.Errorf("enabled order wrong: %v", got)
	}
}

func TestCheckStaleIngestion(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

	t.Run("enabled with zero events is flagged", func(t *testing.T) {
		cfg := Default()
		cfg.VendorUsage.ClaudeCodeJSONL.Enabled = true
		counter := &fakeCounter{counts: map[string]int64{}}
		stale, err := cfg.CheckStaleIngestion(context.Background(), counter, StaleIngestionWindow, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(stale) != 1 || stale[0].SourceTag != "claude-code-jsonl" {
			t.Fatalf("want claude-code-jsonl flagged; got %v", stale)
		}
		if stale[0].WindowHours != 48 {
			t.Errorf("window hours = %d, want 48", stale[0].WindowHours)
		}
	})

	t.Run("enabled with events is not flagged", func(t *testing.T) {
		cfg := Default()
		cfg.VendorUsage.ClaudeCodeJSONL.Enabled = true
		counter := &fakeCounter{counts: map[string]int64{"claude-code-jsonl": 7}}
		stale, err := cfg.CheckStaleIngestion(context.Background(), counter, StaleIngestionWindow, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(stale) != 0 {
			t.Fatalf("source with events should not be flagged; got %v", stale)
		}
	})

	t.Run("disabled source with zero events is not flagged", func(t *testing.T) {
		cfg := Default() // ClaudeCodeJSONL disabled
		counter := &fakeCounter{counts: map[string]int64{}}
		stale, err := cfg.CheckStaleIngestion(context.Background(), counter, StaleIngestionWindow, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(stale) != 0 {
			t.Fatalf("disabled sources must never be flagged; got %v", stale)
		}
	})

	t.Run("window is respected", func(t *testing.T) {
		cfg := Default()
		cfg.VendorUsage.OpenCode.Enabled = true
		counter := &fakeCounter{counts: map[string]int64{}}
		window := 12 * time.Hour
		stale, err := cfg.CheckStaleIngestion(context.Background(), counter, window, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if counter.gotSince != now.Add(-window) || counter.gotUntil != now {
			t.Errorf("window not passed through: since=%v until=%v", counter.gotSince, counter.gotUntil)
		}
		if len(stale) != 1 || stale[0].WindowHours != 12 {
			t.Errorf("want opencode flagged with 12h window; got %v", stale)
		}
	})

	t.Run("non-positive window falls back to default", func(t *testing.T) {
		cfg := Default()
		cfg.VendorUsage.OpenCode.Enabled = true
		counter := &fakeCounter{counts: map[string]int64{}}
		stale, err := cfg.CheckStaleIngestion(context.Background(), counter, 0, now)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if counter.gotSince != now.Add(-StaleIngestionWindow) {
			t.Errorf("default window not applied: since=%v", counter.gotSince)
		}
		if len(stale) != 1 || stale[0].WindowHours != 48 {
			t.Errorf("want default 48h window; got %v", stale)
		}
	})

	t.Run("nil counter yields no warnings and no error", func(t *testing.T) {
		cfg := Default()
		cfg.VendorUsage.ClaudeCodeJSONL.Enabled = true
		stale, err := cfg.CheckStaleIngestion(context.Background(), nil, StaleIngestionWindow, now)
		if err != nil || stale != nil {
			t.Fatalf("nil counter should be a no-op; got stale=%v err=%v", stale, err)
		}
	})

	t.Run("no enabled sources skips the store entirely", func(t *testing.T) {
		cfg := Default()
		counter := &fakeCounter{counts: map[string]int64{}}
		stale, err := cfg.CheckStaleIngestion(context.Background(), counter, StaleIngestionWindow, now)
		if err != nil || stale != nil {
			t.Fatalf("no enabled sources should short-circuit; got stale=%v err=%v", stale, err)
		}
		if counter.callCount != 0 {
			t.Errorf("store should not be queried when nothing is enabled; calls=%d", counter.callCount)
		}
	})

	t.Run("counter error propagates", func(t *testing.T) {
		cfg := Default()
		cfg.VendorUsage.ClaudeCodeJSONL.Enabled = true
		counter := &fakeCounter{err: errors.New("boom")}
		_, err := cfg.CheckStaleIngestion(context.Background(), counter, StaleIngestionWindow, now)
		if err == nil {
			t.Fatal("expected error to propagate")
		}
	})
}

// The warning + next-action strings are contract (asserted verbatim by
// the MCP and CLI status tests); pin their shape here too.
//
// The remedy changed with the wording: the old text said to restart the MCP
// serve process, which does not ingest anything. During the 27-day outage
// eleven `tokenops serve` instances were running and the hint pointed at them,
// while the missing piece was `tokenops start`.
func TestStaleSourceWarning(t *testing.T) {
	s := StaleSource{
		Name: "claude_code_jsonl", SourceTag: "claude-code-jsonl",
		WindowHours: 48, SilentFor: 27 * 24 * time.Hour,
	}
	want := "ingestion stale [critical]: claude-code-jsonl has produced no events for 27 days (checked a 48h window) — if you've been using it, start the ingestion daemon ('tokenops start') — note that 'tokenops serve' is the MCP server and does not ingest"
	if got := s.Warning(); got != want {
		t.Errorf("Warning() = %q\nwant %q", got, want)
	}
}

// The warning read "0 events in the last 48h" on the second day of an outage
// and on the twenty-seventh, so nothing conveyed that the gap was growing. A
// real incident ran 27 days while the text never changed. It now states how
// long the source has actually been silent.
func TestStaleSourceWarningStatesTheRealGap(t *testing.T) {
	tests := []struct {
		name     string
		since    time.Duration
		contains string
		absent   string
	}{
		{"just past the window", 50 * time.Hour, "2 days", "27 days"},
		{"a week", 8 * 24 * time.Hour, "8 days", ""},
		{"the incident", 27 * 24 * time.Hour, "27 days", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := StaleSource{
				Name:        "claude_code_jsonl",
				SourceTag:   "claude-code-jsonl",
				WindowHours: 48,
				SilentFor:   tt.since,
			}
			got := s.Warning()
			if !strings.Contains(got, tt.contains) {
				t.Errorf("warning does not state the gap %q: %s", tt.contains, got)
			}
			if tt.absent != "" && strings.Contains(got, tt.absent) {
				t.Errorf("warning wrongly claims %q: %s", tt.absent, got)
			}
		})
	}
}

// A source that has never produced an event is a different fact from one that
// stopped, and saying "silent for 20000 days" would be nonsense.
func TestStaleSourceWarningWhenNothingWasEverIngested(t *testing.T) {
	s := StaleSource{Name: "claude_code_jsonl", SourceTag: "claude-code-jsonl", WindowHours: 48}

	got := s.Warning()
	if !strings.Contains(got, "no events have ever") {
		t.Errorf("a never-ingested source is not distinguished: %s", got)
	}
}

// Severity has to escalate, or a warning that never changes gets tuned out —
// which is what happened.
func TestStaleSourceSeverityEscalatesWithTheGap(t *testing.T) {
	for _, tt := range []struct {
		silent time.Duration
		want   string
	}{
		{50 * time.Hour, "warning"},
		{8 * 24 * time.Hour, "degraded"},
		{27 * 24 * time.Hour, "critical"},
	} {
		got := StaleSource{SilentFor: tt.silent}.Severity()
		if got != tt.want {
			t.Errorf("silent for %s → severity %q, want %q", tt.silent, got, tt.want)
		}
	}
}

// fakeSeer is a counter that can also report last-seen times.
type fakeSeer struct {
	counts map[string]int64
	last   map[string]time.Time
}

func (f *fakeSeer) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return f.counts, nil
}
func (f *fakeSeer) LastEventBySource(context.Context) (map[string]time.Time, error) {
	return f.last, nil
}

// The gap has to reach the warning, or the escalation is decoration.
func TestCheckStaleIngestionCarriesTheRealGap(t *testing.T) {
	cfg := Config{}
	cfg.VendorUsage.ClaudeCodeJSONL.Enabled = true

	now := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	seer := &fakeSeer{
		counts: map[string]int64{},
		last:   map[string]time.Time{"claude-code-jsonl": now.Add(-27 * 24 * time.Hour)},
	}

	stale, err := cfg.CheckStaleIngestion(context.Background(), seer, StaleIngestionWindow, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 {
		t.Fatalf("got %d stale sources, want 1", len(stale))
	}
	if days := int(stale[0].SilentFor.Hours() / 24); days != 27 {
		t.Errorf("SilentFor = %v (%d days), want 27 days", stale[0].SilentFor, days)
	}
	if got := stale[0].Severity(); got != "critical" {
		t.Errorf("severity = %q, want critical after 27 days", got)
	}
}

// A store that cannot answer the last-seen question must still warn — losing
// the gap is acceptable, losing the warning is not.
func TestCheckStaleIngestionWithoutLastSeenStillWarns(t *testing.T) {
	cfg := Config{}
	cfg.VendorUsage.ClaudeCodeJSONL.Enabled = true

	stale, err := cfg.CheckStaleIngestion(context.Background(), &fakeCounter{counts: map[string]int64{}}, StaleIngestionWindow, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 {
		t.Fatalf("got %d stale sources, want 1", len(stale))
	}
	if stale[0].SilentFor != 0 {
		t.Errorf("SilentFor = %v, want zero when unknown", stale[0].SilentFor)
	}
}
