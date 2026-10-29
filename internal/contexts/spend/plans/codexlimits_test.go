package plans

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A Codex account can hold several limits (one per model family). Each
// rollout token_count and each app-server reading names its own
// limit_id, so two limits must stay two windows, named apart.
func TestVendorWindowsKeepsEachCodexLimitApart(t *testing.T) {
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	def := &eventschema.Envelope{Source: "codex-jsonl", Timestamp: now.Add(-5 * time.Minute), Attributes: map[string]string{
		"primary_used_pct": "14.00", "primary_window_min": "10080", "primary_resets_at": "1791607286",
		"secondary_used_pct": "0.00", "secondary_window_min": "0", "secondary_resets_at": "0",
	}}
	spark := &eventschema.Envelope{Source: "codex-jsonl", Timestamp: now.Add(-time.Minute), Attributes: map[string]string{
		"limit_id": "codex_secondary", "limit_name": "Example Model",
		"primary_used_pct": "7.00", "primary_window_min": "300", "primary_resets_at": "1791600000",
		"secondary_used_pct": "0.00", "secondary_window_min": "0", "secondary_resets_at": "0",
	}}
	r := authFakeReader{events: []*eventschema.Envelope{def, spark}}

	got := VendorWindows(context.Background(), r, eventschema.ProviderOpenAI, now)
	if len(got) != 2 {
		t.Fatalf("windows = %+v, want the codex week and the spark 5h", got)
	}
	byName := map[string]VendorWindow{}
	for _, w := range got {
		byName[w.Name] = w
	}
	if w, ok := byName["week"]; !ok || w.UsedPct != 14 {
		t.Errorf("default week window = %+v, want 14%% named \"week\"", byName["week"])
	}
	if w, ok := byName["5h (Example Model)"]; !ok || w.UsedPct != 7 || w.VendorLabel != "codex_secondary" {
		t.Errorf("spark 5h window = %+v, want 7%% named \"5h (Example Model)\"", byName["5h (Example Model)"])
	}
}

// Without a limit_name the limit_id is the name.
func TestVendorWindowsNamesACodexLimitByItsIDWhenNoNameIsSent(t *testing.T) {
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	spark := &eventschema.Envelope{Source: "codex-app-server", Timestamp: now.Add(-time.Minute), Attributes: map[string]string{
		"limit_id": "codex_other", "primary_used_pct": "30.00", "primary_window_min": "10080", "primary_resets_at": "1791607286",
	}}
	got := VendorWindows(context.Background(), authFakeReader{events: []*eventschema.Envelope{spark}}, eventschema.ProviderOpenAI, now)
	if len(got) != 1 || got[0].Name != "week (codex_other)" {
		t.Errorf("windows = %+v, want one \"week (codex_other)\"", got)
	}
}

// The session-budget prediction reads the default codex window. A newer
// reading for another limit must not stand in for it.
func TestLatestAuthoritativeWindowIgnoresOtherCodexLimits(t *testing.T) {
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	p, _ := Lookup("gpt-plus") // openai, 5h window
	def := env(now.Add(-10*time.Minute), map[string]string{
		"primary_used_pct": "40.00", "primary_window_min": "300", "primary_resets_at": "1791600000",
	})
	spark := env(now.Add(-time.Minute), map[string]string{
		"limit_id": "codex_secondary", "primary_used_pct": "95.00", "primary_window_min": "300", "primary_resets_at": "1791600000",
	})
	w := LatestAuthoritativeWindow(context.Background(), authFakeReader{events: []*eventschema.Envelope{def, spark}}, eventschema.ProviderOpenAI, p, now)
	if w == nil || w.UsedPct != 40 {
		t.Errorf("authoritative window = %+v, want the default limit at 40%%", w)
	}
}
