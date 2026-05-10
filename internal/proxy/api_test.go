package proxy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/felixgeelhaar/tokenops/internal/analytics"
	"github.com/felixgeelhaar/tokenops/internal/spend"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// seedAnalyticsStore populates a sqlite store with a few PromptEvents
// across distinct workflows so the analytics handlers exercise their
// grouping + summary code paths.
func seedAnalyticsStore(t *testing.T) (*sqlite.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "events.db")
	ctx := context.Background()
	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	now := time.Now().UTC()
	rows := []struct {
		offset    time.Duration
		workflow  string
		model     string
		in, out   int64
		cost      float64
	}{
		{1 * time.Hour, "wf-a", "gpt-4o-mini", 1000, 200, 0.40},
		{2 * time.Hour, "wf-a", "gpt-4o-mini", 800, 150, 0.32},
		{3 * time.Hour, "wf-b", "claude-sonnet-4-6", 1200, 250, 0.90},
	}
	for _, r := range rows {
		env := &eventschema.Envelope{
			ID:            uuid.NewString(),
			SchemaVersion: eventschema.SchemaVersion,
			Type:          eventschema.EventTypePrompt,
			Timestamp:     now.Add(-r.offset),
			Source:        "test",
			Payload: &eventschema.PromptEvent{
				Provider:     eventschema.ProviderOpenAI,
				RequestModel: r.model,
				InputTokens:  r.in,
				OutputTokens: r.out,
				TotalTokens:  r.in + r.out,
				CostUSD:      r.cost,
				Status:       200,
				WorkflowID:   r.workflow,
			},
		}
		if err := store.Append(ctx, env); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	return store, func() { _ = store.Close() }
}

func startAnalyticsProxy(t *testing.T, store *sqlite.Store) string {
	t.Helper()
	spendEng := spend.NewEngine(spend.DefaultTable())
	agg := analytics.New(store, spendEng)
	handlers, err := NewAnalyticsHandlers(store, agg, spendEng)
	if err != nil {
		t.Fatalf("NewAnalyticsHandlers: %v", err)
	}
	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithAnalytics(handlers),
	)
	ctx, cancel := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	})
	waitListening(t, srv.Addr())
	return srv.Addr()
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("Get %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Get %s status=%d body=%s", url, resp.StatusCode, body)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestAPISpendSummary(t *testing.T) {
	store, cleanup := seedAnalyticsStore(t)
	defer cleanup()
	addr := startAnalyticsProxy(t, store)
	got := getJSON(t, "http://"+addr+"/api/spend/summary")
	summary, _ := got["summary"].(map[string]any)
	if summary == nil {
		t.Fatalf("missing summary: %v", got)
	}
	if reqs, _ := summary["Requests"].(float64); reqs != 3 {
		t.Errorf("requests = %v, want 3", summary["Requests"])
	}
}

func TestAPISpendSeriesByWorkflow(t *testing.T) {
	store, cleanup := seedAnalyticsStore(t)
	defer cleanup()
	addr := startAnalyticsProxy(t, store)
	got := getJSON(t, "http://"+addr+"/api/spend/series?bucket=hour&group=workflow&since=24h")
	rows, _ := got["rows"].([]any)
	if len(rows) == 0 {
		t.Fatalf("expected rows: %v", got)
	}
}

func TestAPIWorkflowsList(t *testing.T) {
	store, cleanup := seedAnalyticsStore(t)
	defer cleanup()
	addr := startAnalyticsProxy(t, store)
	got := getJSON(t, "http://"+addr+"/api/workflows?since=24h")
	wfs, _ := got["workflows"].([]any)
	if len(wfs) != 2 {
		t.Errorf("workflows = %d, want 2", len(wfs))
	}
}

func TestAPIWorkflowDetailNotFound(t *testing.T) {
	store, cleanup := seedAnalyticsStore(t)
	defer cleanup()
	addr := startAnalyticsProxy(t, store)
	resp, err := http.Get("http://" + addr + "/api/workflows/missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestAPIForecastEndpoint(t *testing.T) {
	store, cleanup := seedAnalyticsStore(t)
	defer cleanup()
	addr := startAnalyticsProxy(t, store)
	got := getJSON(t, "http://"+addr+"/api/spend/forecast?horizon_days=3")
	if got["horizon_days"].(float64) != 3 {
		t.Errorf("horizon_days = %v", got["horizon_days"])
	}
}
