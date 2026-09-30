package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/planhistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func runPlanCmd(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs(append([]string{"plan"}, args...))
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("plan %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

// A backdated switch records the history, keeps the plan before it, and
// re-marks the usage recorded as billed since the start date.
func TestPlanSetSinceRecordsHistoryAndRestamps(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("plans:\n  openai: gpt-plus\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "events.db")
	ctx := context.Background()
	store, err := sqlite.Open(ctx, dbPath, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	billed := time.Now().UTC().Add(-48 * time.Hour)
	if err := store.Append(ctx, &eventschema.Envelope{
		ID: "billed-1", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: billed, Source: "codex-jsonl",
		Payload: &eventschema.PromptEvent{Provider: "openai", RequestModel: "gpt-5.6-sol"},
	}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	file, _ := planhistory.Default()
	t.Cleanup(func() { _ = os.Remove(file.Path) })

	since := billed.Add(-24 * time.Hour).Format("2006-01-02")
	out := runPlanCmd(t, "set", "openai", "gpt-pro-5x", "--since", since,
		"--config-path", cfgPath, "--db", dbPath, "--no-restart")
	if !strings.Contains(out, "re-marked 1 earlier call(s)") {
		t.Fatalf("output: %s", out)
	}
	// Other tests in this package bind plans in the same sandbox, so
	// read only this provider's entries.
	all, err := file.Load()
	var h []string
	for _, b := range all {
		if b.Provider == "openai" {
			h = append(h, b.Plan)
		}
	}
	if err != nil || strings.Join(h, ",") != "gpt-plus,gpt-pro-5x" {
		t.Fatalf("history = %v, %v", h, err)
	}
	if hist := runPlanCmd(t, "history"); !strings.Contains(hist, "gpt-pro-5x") || !strings.Contains(hist, "from "+since) {
		t.Errorf("plan history: %s", hist)
	}

	store, err = sqlite.Open(ctx, dbPath, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	envs, err := store.Query(ctx, sqlite.Filter{Type: eventschema.EventTypePrompt})
	if err != nil || len(envs) != 1 {
		t.Fatalf("envs=%v err=%v", envs, err)
	}
	if p := envs[0].Payload.(*eventschema.PromptEvent); p.CostSource != eventschema.CostSourcePlanIncluded {
		t.Errorf("cost source = %q", p.CostSource)
	}
}

func TestPlanCatalogShowsPrices(t *testing.T) {
	out := runPlanCmd(t, "catalog")
	if !strings.Contains(out, "$100/month") || !strings.Contains(out, "per seat") || !strings.Contains(out, "no flat price") {
		t.Errorf("catalog:\n%s", out)
	}
}

func TestSpendTextShowsPlanCostAndValue(t *testing.T) {
	var out bytes.Buffer
	v := spendView{Window: "last 30d", Currency: "USD"}
	v.Summary.APIEquivalentUSD = 11400
	fillPlanCost(&v, map[string]string{"anthropic": "claude-max-20x"}, "EUR", 0.85,
		time.Now().Add(-30*24*time.Hour), time.Now())
	if err := writeSpendText(&out, v); err != nil {
		t.Fatal(err)
	}
	// No price given: the list price, converted at the operator's rate.
	text := out.String()
	if !strings.Contains(text, "EUR (prorated; US list price where you gave none)") || !strings.Contains(text, "value per plan EUR:") {
		t.Errorf("spend text:\n%s", text)
	}
}

// A price given with --price is recorded and shown in plan history.
func TestPlanSetRecordsThePrice(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("money:\n  currency: EUR\nplans:\n  anthropic: claude-max-20x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runPlanCmd(t, "set", "anthropic", "claude-max-20x", "--price", "214.60",
		"--config-path", cfgPath, "--db", filepath.Join(dir, "events.db"), "--no-restart")
	if hist := runPlanCmd(t, "history"); !strings.Contains(hist, "at 214.60 EUR/month") {
		t.Errorf("plan history:\n%s", hist)
	}
}
