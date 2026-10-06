package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func meterStore(t *testing.T, at time.Time, attrs map[string]string, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := sqlite.Open(context.Background(), path, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	env := &eventschema.Envelope{
		ID: "m1", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: at, Source: source, Attributes: attrs,
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
	}
	if err := s.Append(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLiveQuotaReadsTheMostConstrainedWindow(t *testing.T) {
	now := time.Now()
	path := meterStore(t, now.Add(-3*time.Minute), map[string]string{
		"five_hour_kind": "session", "five_hour_used_pct": "12.00", "five_hour_reset_at": now.Add(2 * time.Hour).Format(time.RFC3339Nano),
		"seven_day_kind": "weekly_all", "seven_day_used_pct": "81.00", "seven_day_reset_at": now.Add(48 * time.Hour).Format(time.RFC3339Nano),
	}, "claude-usage-meter")
	cfg := config.Config{Plans: map[string]string{"anthropic": "claude-max-20x"}, Storage: config.StorageConfig{Enabled: true, Path: path}}
	q := readPlan(context.Background(), cfg, eventschema.ProviderAnthropic, now).Quota
	if q == nil || q.Window.Label != "weekly" || q.Window.UsedPct != 81 {
		t.Fatalf("liveQuota = %+v, want the 81%% weekly window", q)
	}
}

func TestLiveQuotaNeedsAPlanAndAFreshReading(t *testing.T) {
	now := time.Now()
	attrs := map[string]string{"five_hour_kind": "session", "five_hour_used_pct": "50.00", "five_hour_reset_at": now.Add(time.Hour).Format(time.RFC3339Nano)}
	fresh := meterStore(t, now.Add(-time.Minute), attrs, "claude-usage-meter")
	stale := meterStore(t, now.Add(-2*time.Hour), attrs, "claude-usage-meter")
	for name, cfg := range map[string]config.Config{
		"no plan":       {Storage: config.StorageConfig{Enabled: true, Path: fresh}},
		"unknown plan":  {Plans: map[string]string{"anthropic": "claude-ultra"}, Storage: config.StorageConfig{Enabled: true, Path: fresh}},
		"stale reading": {Plans: map[string]string{"anthropic": "claude-max-20x"}, Storage: config.StorageConfig{Enabled: true, Path: stale}},
		"no store":      {Plans: map[string]string{"anthropic": "claude-max-20x"}, Storage: config.StorageConfig{Enabled: true, Path: filepath.Join(t.TempDir(), "absent.db")}},
	} {
		if q := readPlan(context.Background(), cfg, eventschema.ProviderAnthropic, now).Quota; q != nil {
			t.Errorf("%s: liveQuota = %+v, want no live window", name, q)
		}
	}
}

// A flat plan whose reading stopped says how old it is and how to get it
// back; one never set up says nothing; pay-as-you-go is not flat.
func TestReadPlanSaysWhyTheReadingIsLost(t *testing.T) {
	now := time.Now()
	attrs := map[string]string{"five_hour_kind": "session", "five_hour_used_pct": "50.00", "five_hour_reset_at": now.Add(time.Hour).Format(time.RFC3339Nano)}
	stale := meterStore(t, now.Add(-20*time.Hour-time.Minute), attrs, "claude-usage-meter")
	empty := meterStore(t, now, map[string]string{"other": "x"}, "claude-code-jsonl")
	max := map[string]string{"anthropic": "claude-max-20x"}

	r := readPlan(context.Background(), config.Config{Plans: max, Storage: config.StorageConfig{Enabled: true, Path: stale}}, eventschema.ProviderAnthropic, now)
	if r.Quota != nil || !r.Flat || !strings.Contains(r.Lost, "20h old") || !strings.Contains(r.Lost, "vendor-usage setup claude-subscription") {
		t.Errorf("stale: %+v", r)
	}
	r = readPlan(context.Background(), config.Config{Plans: max, Storage: config.StorageConfig{Enabled: true, Path: empty}}, eventschema.ProviderAnthropic, now)
	if !r.Flat || r.Lost != "" {
		t.Errorf("never set up: %+v", r)
	}
	payg := map[string]string{"anthropic": "pay-as-you-go"}
	if r = readPlan(context.Background(), config.Config{Plans: payg, Storage: config.StorageConfig{Enabled: true, Path: stale}}, eventschema.ProviderAnthropic, now); r.Flat || r.Lost != "" {
		t.Errorf("pay as you go: %+v", r)
	}
}

func TestHookProvider(t *testing.T) {
	cases := []struct {
		in             stopHookInput
		cursor, opencd bool
		want           eventschema.Provider
		ok             bool
	}{
		{stopHookInput{TranscriptPath: "/Users/x/.claude/projects/p/s.jsonl"}, false, false, eventschema.ProviderAnthropic, true},
		{stopHookInput{TranscriptPath: "/Users/x/.codex/sessions/2026/09/27/rollout-a.jsonl"}, false, false, eventschema.ProviderOpenAI, true},
		{stopHookInput{}, true, false, "", false},
		{stopHookInput{}, false, true, "", false},
	}
	for _, c := range cases {
		got, ok := hookProvider(c.in, c.cursor, c.opencd)
		if got != c.want || ok != c.ok {
			t.Errorf("hookProvider(%q) = %q,%v want %q,%v", c.in.TranscriptPath, got, ok, c.want, c.ok)
		}
	}
}
