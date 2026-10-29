package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// seedFocusExportFixture writes two days of September 2026 usage, plan
// covered and metered, and a config binding Anthropic to Max 5x. HOME is
// a fresh directory, so no plan history from another test applies.
func seedFocusExportFixture(t *testing.T) (dbPath, cfgPath string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgPath = filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("plans:\n  anthropic: claude-max-5x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath = filepath.Join(home, "events.db")
	ctx := context.Background()
	store, err := sqlite.Open(ctx, dbPath, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	ev := func(id string, at time.Time, provider eventschema.Provider, model string, in, out int64, cost float64, source eventschema.CostSource) *eventschema.Envelope {
		return &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: at, Source: "claude-code-jsonl",
			Payload: &eventschema.PromptEvent{
				PromptHash: "sha256:" + id, Provider: provider, RequestModel: model,
				InputTokens: in, OutputTokens: out, TotalTokens: in + out,
				CostUSD: cost, CostSource: source,
				// A session and a workflow the export must not carry.
				SessionID: "session-secret-title", WorkflowID: "wf-/Users/someone/private-repo",
			},
		}
	}
	if err := store.AppendBatch(ctx, []*eventschema.Envelope{
		ev("a", day.Add(9*time.Hour), eventschema.ProviderAnthropic, "claude-opus-5", 2_000_000, 200_000, 0, eventschema.CostSourcePlanIncluded),
		ev("b", day.Add(10*time.Hour), eventschema.ProviderAnthropic, "claude-sonnet-5", 500_000, 50_000, 0, eventschema.CostSourcePlanIncluded),
		ev("c", day.Add(33*time.Hour), eventschema.ProviderOpenAI, "gpt-4o", 10_000, 2_000, 0.045, eventschema.CostSourceMetered),
	}); err != nil {
		t.Fatal(err)
	}
	return dbPath, cfgPath
}

// TestSpendExportFocusGolden pins `tokenops spend export --format focus`
// end to end, in each FOCUS version: the store and the plan binding in,
// FOCUS CSV out.
func TestSpendExportFocusGolden(t *testing.T) {
	for _, tc := range []struct{ version, golden string }{
		{"", "spend/focus-1.4.csv"},
		{"1.2", "spend/focus.csv"},
	} {
		t.Run(tc.golden, func(t *testing.T) {
			db, cfg := seedFocusExportFixture(t)
			args := []string{"spend", "export", "--format", "focus", "--db", db, "--config", cfg,
				"--since", "2026-09-01T00:00:00Z", "--until", "2026-09-03T00:00:00Z"}
			if tc.version != "" {
				args = append(args, "--focus-version", tc.version)
			}
			out, err := executeRoot(t, args...)
			if err != nil {
				t.Fatalf("export: %v", err)
			}
			assertGolden(t, tc.golden, out)
			for _, private := range []string{"session-secret-title", "private-repo", "sha256:"} {
				if strings.Contains(out, private) {
					t.Errorf("the export carries %q; only derived fields may leave", private)
				}
			}
		})
	}
}

func TestSpendExportRefusesAnUnknownVersion(t *testing.T) {
	db, cfg := seedFocusExportFixture(t)
	_, err := executeRoot(t, "spend", "export", "--focus-version", "1.3", "--db", db, "--config", cfg)
	if err == nil || !strings.Contains(err.Error(), "1.4") {
		t.Fatalf("want an error naming the supported versions, got %v", err)
	}
}

func TestSpendExportWritesTheFileItIsGiven(t *testing.T) {
	db, cfg := seedFocusExportFixture(t)
	path := filepath.Join(t.TempDir(), "focus.csv")
	out, err := executeRootStreams(t, "spend", "export", "--db", db, "--config", cfg,
		"--since", "2026-09-01T00:00:00Z", "--until", "2026-09-03T00:00:00Z", "--output", path)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "BilledCost,BillingAccountId,") {
		t.Errorf("the file is not the FOCUS CSV:\n%s", data)
	}
	if !strings.Contains(out, "wrote "+path) || !strings.Contains(out, "FOCUS 1.4") {
		t.Errorf("say where it went and what it is:\n%s", out)
	}
}

func TestSpendExportRefusesAnUnknownFormat(t *testing.T) {
	db, cfg := seedFocusExportFixture(t)
	_, err := executeRoot(t, "spend", "export", "--format", "parquet", "--db", db, "--config", cfg)
	if err == nil || !strings.Contains(err.Error(), "focus") {
		t.Fatalf("want an error naming the supported format, got %v", err)
	}
}
