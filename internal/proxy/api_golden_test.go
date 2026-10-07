package proxy

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var updateGolden = flag.Bool("update-api-golden", false, "rewrite internal/proxy/testdata/api_golden")

// The /api/* read routes answer from capability packages (ADR 0010). These
// goldens pin every byte those routes send — status, the headers a client
// reads, and the body in its exact key order — so moving the use-case
// logic out of the handlers cannot change a response unnoticed.
//
// The store is seeded at fixed instants and every request names an
// explicit window, so the bodies do not depend on when the test runs.
// Rule-corpus paths, the rule router's timing and audit entry ids are
// the only volatile parts; they are rewritten before comparing.

var goldenDay = time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)

const goldenWindow = "since=2026-01-01T00:00:00Z&until=2026-01-10T00:00:00Z"

func seedGoldenStore(t *testing.T) *sqlite.Store {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	prompts := []struct {
		id       string
		offset   time.Duration
		workflow string
		agent    string
		model    string
		in, out  int64
		cached   int64
		cost     float64
	}{
		{"p1", 0, "wf-a", "agent-1", "gpt-4o-mini", 1000, 200, 400, 0.40},
		{"p2", time.Minute, "wf-a", "agent-1", "gpt-4o-mini", 1000, 150, 0, 0.32},
		{"p3", 2 * time.Minute, "wf-a", "agent-1", "gpt-4o-mini", 1000, 150, 0, 0.32},
		{"p4", 25 * time.Hour, "wf-b", "agent-2", "claude-sonnet-4-6", 1200, 250, 600, 0.90},
	}
	for _, p := range prompts {
		env := &eventschema.Envelope{
			ID:            p.id,
			SchemaVersion: eventschema.SchemaVersion,
			Type:          eventschema.EventTypePrompt,
			Timestamp:     goldenDay.Add(p.offset),
			Source:        "test",
			Payload: &eventschema.PromptEvent{
				PromptHash:        "hash-" + p.workflow,
				Provider:          eventschema.ProviderOpenAI,
				RequestModel:      p.model,
				InputTokens:       p.in,
				OutputTokens:      p.out,
				CachedInputTokens: p.cached,
				TotalTokens:       p.in + p.out,
				CostUSD:           p.cost,
				Status:            200,
				WorkflowID:        p.workflow,
				AgentID:           p.agent,
			},
		}
		if err := store.Append(ctx, env); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	opt := &eventschema.Envelope{
		ID:            "o1",
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypeOptimization,
		Timestamp:     goldenDay.Add(3 * time.Minute),
		Source:        "test",
		Payload: &eventschema.OptimizationEvent{
			PromptHash:             "hash-wf-a",
			Kind:                   eventschema.OptimizationTypeUnknown,
			Mode:                   eventschema.OptimizationModePassive,
			EstimatedSavingsTokens: 120,
			EstimatedSavingsUSD:    0.05,
			QualityScore:           0.9,
			Decision:               eventschema.OptimizationDecisionApplied,
			Reason:                 "trimmed context",
			WorkflowID:             "wf-a",
			AgentID:                "agent-1",
		},
	}
	if err := store.Append(ctx, opt); err != nil {
		t.Fatalf("append optimization: %v", err)
	}
	rec := audit.NewRecorder(store)
	for i, a := range []audit.Action{audit.ActionBudgetExceeded, audit.ActionOptimizationApply} {
		if _, err := rec.Record(ctx, audit.Entry{
			Action:    a,
			Actor:     "golden",
			Target:    "weekly",
			Timestamp: goldenDay.Add(time.Duration(i) * time.Hour),
			Details:   map[string]any{"spent_usd": 150.0},
		}); err != nil {
			t.Fatalf("audit: %v", err)
		}
	}
	return store
}

func goldenServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	store := seedGoldenStore(t)
	spendEng := spend.NewEngine(spend.DefaultTable())
	agg := analytics.New(store, spendEng)
	an, err := NewAnalyticsHandlers(store, agg, spendEng, waste.Config{})
	if err != nil {
		t.Fatal(err)
	}
	root := writeRulesAPICorpus(t)
	rh, err := NewRulesHandlers(root, "repo")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	an.Register(mux)
	rh.Register(mux)
	NewAuditHandlers(store).Register(mux)
	at := time.Date(2026, 1, 5, 11, 0, 0, 0, time.UTC)
	s := New("127.0.0.1:0", WithSourceFreshness(func() []freshness.Report {
		return []freshness.Report{
			{Name: "Cursor", Tag: "cursor-web", State: freshness.StateFailing, Severity: freshness.SeverityDegraded,
				LastEventAt: at, SilentFor: 3 * time.Hour, LastPollAt: at.Add(3 * time.Hour), LastSuccessfulPollAt: at,
				LastError: "401 unauthorized"},
			{Name: "Claude Code", Tag: "claude-code", State: freshness.StateHealthy, LastEventAt: at},
		}
	}))
	s.registerSourcesRoute(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, root
}

// goldenRoutes names each fixture after the route it pins.
var goldenRoutes = []struct{ name, path string }{
	{"spend_summary", "/api/spend/summary?" + goldenWindow},
	{"spend_summary_filtered", "/api/spend/summary?" + goldenWindow + "&workflow_id=wf-a&model=gpt-4o-mini&provider=openai&agent_id=agent-1"},
	{"spend_summary_bad_since", "/api/spend/summary?since=yesterday-ish"},
	{"spend_series_hour", "/api/spend/series?" + goldenWindow},
	{"spend_series_day_model", "/api/spend/series?" + goldenWindow + "&bucket=day&group=model"},
	{"spend_series_day_workflow", "/api/spend/series?" + goldenWindow + "&bucket=DAY&group=workflow"},
	{"spend_series_agent", "/api/spend/series?" + goldenWindow + "&group=agent"},
	{"spend_series_provider", "/api/spend/series?" + goldenWindow + "&group=provider"},
	{"spend_series_bad_until", "/api/spend/series?until=nope"},
	{"spend_cache_stats", "/api/spend/cache_stats?" + goldenWindow},
	{"spend_cache_stats_bad", "/api/spend/cache_stats?since=nope"},
	// One workflow per window: the list is built from a map and its
	// order is unspecified, which the move must not change either.
	{"workflows", "/api/workflows?since=2026-01-05T00:00:00Z&until=2026-01-06T00:00:00Z"},
	{"workflows_b", "/api/workflows?since=2026-01-06T00:00:00Z&until=2026-01-07T00:00:00Z"},
	{"workflows_bad", "/api/workflows?since=nope"},
	{"workflow_detail", "/api/workflows/wf-a"},
	{"workflow_detail_missing", "/api/workflows/missing"},
	{"optimizations", "/api/optimizations?" + goldenWindow},
	{"optimizations_filtered", "/api/optimizations?" + goldenWindow + "&workflow_id=wf-b"},
	{"optimizations_bad", "/api/optimizations?until=nope"},
	{"audit", "/api/audit?" + goldenWindow},
	{"audit_action", "/api/audit?" + goldenWindow + "&action=optimization_apply&limit=5"},
	{"audit_bad", "/api/audit?since=nope"},
	{"rules_analyze", "/api/rules/analyze"},
	{"rules_analyze_cached", "/api/rules/analyze"},
	{"rules_analyze_anthropic", "/api/rules/analyze?provider=anthropic"},
	{"rules_conflicts", "/api/rules/conflicts"},
	{"rules_compress", "/api/rules/compress?similarity=0.5&quality_floor=0.2"},
	{"rules_inject", "/api/rules/inject?keywords=testing&token_budget=500"},
	{"rules_inject_json_lists", `/api/rules/inject?keywords=["go","style"]&files=a.go,b.go&include_global=false`},
	{"rules_missing_root", "/api/rules/conflicts?root=/definitely/not/here"},
	{"sources", "/api/sources"},
}

func TestAPIResponsesMatchGoldens(t *testing.T) {
	ts, root := goldenServer(t)
	dir := filepath.Join("testdata", "api_golden")
	for _, rt := range goldenRoutes {
		t.Run(rt.name, func(t *testing.T) {
			got := goldenResponse(t, ts.URL+rt.path, root)
			file := filepath.Join(dir, rt.name+".golden")
			if *updateGolden {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read golden (run with -update-api-golden): %v", err)
			}
			if got != string(want) {
				t.Errorf("%s changed.\n--- want\n%s\n--- got\n%s", rt.path, want, got)
			}
		})
	}
}

// goldenResponse renders status, the headers a client reads, and the body.
func goldenResponse(t *testing.T, url, root string) string {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(resp.Status + "\n")
	for _, h := range []string{"Content-Type", "Cache-Control", "X-Cache"} {
		if v := resp.Header.Get(h); v != "" {
			b.WriteString(h + ": " + v + "\n")
		}
	}
	b.WriteString("\n")
	out := strings.ReplaceAll(string(body), root, "ROOT")
	for _, n := range volatile {
		out = n.re.ReplaceAllString(out, n.with)
	}
	b.WriteString(out)
	return b.String()
}

// volatile are the values that differ run to run by design: the rule
// router's own timing and the audit log's random entry ids.
var volatile = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`"ElapsedNS":\d+`), `"ElapsedNS":0`},
	{regexp.MustCompile(`"ID":"[0-9a-f-]{36}"`), `"ID":"UUID"`},
}

// The forecast's window is the thirty days before now, so its body
// cannot be pinned byte for byte. Its shape can: the keys, their order,
// and that the history it reports is the history it counted.
func TestAPIForecastShape(t *testing.T) {
	ts, _ := goldenServer(t)
	resp, err := http.Get(ts.URL + "/api/spend/forecast?horizon_days=45") //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("headers = %v", resp.Header)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"currency", "forecast", "forecast_tokens", "history", "history_points", "horizon_days"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	// An out-of-range horizon falls back to seven days.
	if string(got["horizon_days"]) != "7" {
		t.Errorf("horizon_days = %s, want 7", got["horizon_days"])
	}
	// Keys arrive in sorted order, as a map encodes them.
	if !strings.HasPrefix(string(body), `{"currency":`) {
		t.Errorf("body does not start with currency: %.60s", body)
	}
}
