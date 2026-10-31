package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// planAdviseStore writes four weeks on Ultra peaking at 20% of each
// weekly window, with a plan-covered Sonnet turn a day, and a config
// binding the plan. It returns the config and store paths.
func planAdviseStore(t *testing.T) (cfgPath, db string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	db = filepath.Join(dir, "events.db")
	st, err := sqlite.Open(context.Background(), db, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	since := now.Add(-28 * 24 * time.Hour)
	var envs []*eventschema.Envelope
	n := 0
	add := func(e *eventschema.Envelope) {
		n++
		e.ID, e.SchemaVersion, e.Type = fmt.Sprintf("advise-%d", n), eventschema.SchemaVersion, eventschema.EventTypePrompt
		envs = append(envs, e)
	}
	for i := range 4 {
		reset := since.Add(time.Duration(i+1) * 7 * 24 * time.Hour)
		start := reset.Add(-7 * 24 * time.Hour)
		for _, r := range []struct {
			at   time.Duration
			used string
		}{{24 * time.Hour, "5.00"}, {5 * 24 * time.Hour, "20.00"}} {
			add(&eventschema.Envelope{Source: "claude-usage-meter", Timestamp: start.Add(r.at),
				Attributes: map[string]string{"granularity": "quota_snapshot", "seven_day_kind": "weekly_all",
					"seven_day_used_pct": r.used, "seven_day_reset_at": reset.Format(time.RFC3339)},
				Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, Status: 200}})
		}
	}
	for i := range 28 {
		add(&eventschema.Envelope{Source: "claude-code-jsonl", Timestamp: since.Add(time.Duration(i)*24*time.Hour + time.Hour),
			Attributes: map[string]string{"granularity": "assistant_turn"},
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-4-6",
				InputTokens: 2_000_000, OutputTokens: 200_000, CostSource: eventschema.CostSourcePlanIncluded, Status: 200}})
	}
	if err := st.AppendBatch(context.Background(), envs); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	cfgPath = filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("plans:\n  anthropic: claude-ultra\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath, db
}

func TestPlanAdvise(t *testing.T) {
	cfgPath, db := planAdviseStore(t)
	out, err := executeRoot(t, "--config", cfgPath, "plan", "advise", "--db", db)
	if err != nil {
		t.Fatalf("plan advise: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Claude Ultra", "Claude Max", "API at list price", "LOCKOUTS", "peaked", "confidence low", "no 5-hour limit",
		"unverified", "TokenOps never switches a plan",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	out, err = executeRoot(t, "--config", cfgPath, "plan", "advise", "--db", db, "--days", "60", "--json")
	if err != nil {
		t.Fatalf("plan advise --json: %v\n%s", err, out)
	}
	var p headroom.AdvicePayload
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if p.Days != 60 || len(p.Providers) != 1 || p.Providers[0].Recommendation == nil {
		t.Fatalf("payload = %+v", p)
	}
	if r := p.Providers[0].Recommendation; r.Verdict != "switch" || r.Plan != "claude-max" {
		t.Errorf("recommendation = %+v", r)
	}

	// The advice binds nothing.
	cfg, err := os.ReadFile(cfgPath)
	if err != nil || string(cfg) != "plans:\n  anthropic: claude-ultra\n" {
		t.Errorf("config after advise = %q %v", cfg, err)
	}
	if _, err := executeRoot(t, "--config", cfgPath, "plan", "advise", "--db", db, "--days", "91"); err == nil {
		t.Error("--days 91 is refused")
	}
}
