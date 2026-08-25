package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
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

// seedSourceMixDB writes one prompt event per source class so the
// include-source flag has something to re-admit.
func seedSourceMixDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	ctx := context.Background()
	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	for i, src := range []string{"proxy", "demo", "mcp-session"} {
		env := &eventschema.Envelope{
			ID:            uuid.NewString(),
			SchemaVersion: eventschema.SchemaVersion,
			Type:          eventschema.EventTypePrompt,
			Timestamp:     now.Add(-time.Duration(i+1) * time.Hour),
			Source:        src,
			Payload: &eventschema.PromptEvent{
				PromptHash:   "sha256:abc",
				Provider:     eventschema.ProviderOpenAI,
				RequestModel: "model-" + src,
				InputTokens:  100,
				OutputTokens: 10,
				TotalTokens:  110,
				Status:       200,
				CostUSD:      1,
			},
		}
		if err := store.Append(ctx, env); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	return path
}

// spendRequests runs the spend command with the given extra flags and
// returns the headline request count.
func spendRequests(t *testing.T, path string, extra ...string) float64 {
	t.Helper()
	args := append([]string{"spend", "--db", path, "--json"}, extra...)
	out, err := executeRoot(t, args...)
	if err != nil {
		t.Fatalf("spend %v: %v", extra, err)
	}
	var parsed struct {
		Summary struct {
			Requests float64 `json:"Requests"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	return parsed.Summary.Requests
}

func TestSpendIncludeSourceReadmitsOnlyNamed(t *testing.T) {
	path := seedSourceMixDB(t)
	cases := []struct {
		name  string
		extra []string
		want  float64
	}{
		{"default drops synthetic and activity proxy", nil, 1},
		{"demo only", []string{"--include-source", "demo"}, 2},
		{"activity proxy only", []string{"--include-source", "mcp-session"}, 2},
		{"comma separated", []string{"--include-source", "demo,mcp-session"}, 3},
		{"repeated flag", []string{"--include-source", "demo", "--include-source", "mcp-session"}, 3},
		{"back-compat alias", []string{"--include-demo"}, 2},
		{"alias composes with flag", []string{"--include-demo", "--include-source", "mcp-session"}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := spendRequests(t, path, tc.extra...); got != tc.want {
				t.Errorf("requests = %v; want %v", got, tc.want)
			}
		})
	}
}
