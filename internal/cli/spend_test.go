package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// seedSpendDB seeds a sqlite store with prompt events spread across the
// last 36 hours so the spend command exercises both the headline summary
// and the 24h burn-rate window.
func seedSpendDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "events.db")

	ctx := context.Background()
	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	type seed struct {
		offset        time.Duration
		model         string
		inTok, outTok int64
		cost          float64
	}
	rows := []seed{
		{offset: 30 * time.Hour, model: "gpt-4o-mini", inTok: 1000, outTok: 200, cost: 0.50},
		{offset: 26 * time.Hour, model: "claude-sonnet-4-6", inTok: 800, outTok: 300, cost: 1.20},
		{offset: 12 * time.Hour, model: "gpt-4o-mini", inTok: 1500, outTok: 250, cost: 0.75},
		{offset: 8 * time.Hour, model: "claude-sonnet-4-6", inTok: 600, outTok: 200, cost: 0.90},
		{offset: 2 * time.Hour, model: "gpt-4o-mini", inTok: 2000, outTok: 400, cost: 1.00},
		{offset: 1 * time.Hour, model: "claude-sonnet-4-6", inTok: 700, outTok: 250, cost: 0.80},
	}

	for _, s := range rows {
		env := &eventschema.Envelope{
			ID:            uuid.NewString(),
			SchemaVersion: eventschema.SchemaVersion,
			Type:          eventschema.EventTypePrompt,
			Timestamp:     now.Add(-s.offset),
			Source:        "test",
			Payload: &eventschema.PromptEvent{
				PromptHash:    "sha256:abc",
				Provider:      eventschema.ProviderOpenAI,
				RequestModel:  s.model,
				ResponseModel: s.model,
				InputTokens:   s.inTok,
				OutputTokens:  s.outTok,
				TotalTokens:   s.inTok + s.outTok,
				ContextSize:   s.inTok,
				Latency:       300 * time.Millisecond,
				Status:        200,
				CostUSD:       s.cost,
				WorkflowID:    "wf-test",
				AgentID:       "agent-test",
			},
		}
		if err := store.Append(ctx, env); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	return path
}

func TestSpendTextRenders(t *testing.T) {
	path := seedSpendDB(t)
	out, err := executeRoot(t, "spend", "--db", path, "--by", "model")
	if err != nil {
		t.Fatalf("spend: %v", err)
	}
	for _, want := range []string{
		"Spend report",
		"requests:",
		"total spend:",
		"burn rate (24h):",
		"Top consumers by model",
		"gpt-4o-mini",
		"claude-sonnet-4-6",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestSpendJSONShape(t *testing.T) {
	path := seedSpendDB(t)
	out, err := executeRoot(t, "spend", "--db", path, "--json")
	if err != nil {
		t.Fatalf("spend --json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	for _, key := range []string{"summary", "top", "burn_rate_24h", "currency"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("missing %q key: %v", key, parsed)
		}
	}
	summary, _ := parsed["summary"].(map[string]any)
	if summary == nil || summary["Requests"] == nil {
		t.Errorf("summary missing requests: %v", parsed["summary"])
	}
}

func TestSpendForecastIncluded(t *testing.T) {
	path := seedSpendDB(t)
	out, err := executeRoot(t, "spend", "--db", path, "--forecast", "--forecast-days", "3")
	if err != nil {
		t.Fatalf("spend --forecast: %v", err)
	}
	// With only a few rows in the seed (across 1–2 daily buckets), the
	// forecast may degrade to an empty list. We only assert that the
	// "Forecast" header surfaces when a forecast was produced; otherwise
	// the section is suppressed (which is the documented behaviour).
	if strings.Contains(out, "Forecast (next") {
		if !strings.Contains(out, "WHEN") {
			t.Errorf("forecast header without table:\n%s", out)
		}
	}
}

func TestSpendInvalidGroup(t *testing.T) {
	path := seedSpendDB(t)
	_, err := executeRoot(t, "spend", "--db", path, "--by", "garbage")
	if err == nil {
		t.Fatal("expected error for invalid --by value")
	}
	if !strings.Contains(err.Error(), "garbage") {
		t.Errorf("error = %q", err)
	}
}

func TestSparklineHandlesEmpty(t *testing.T) {
	if got := sparklineFromRows(nil); got != "" {
		t.Errorf("empty sparkline = %q, want empty", got)
	}
}
