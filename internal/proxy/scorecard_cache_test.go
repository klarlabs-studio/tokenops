package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// scorecardServer serves the analytics routes over store, the scorecard
// reading events through events when it is not nil.
func scorecardServer(t *testing.T, store *sqlite.Store, events *sqlite.EventCache) string {
	t.Helper()
	spendEng := spend.NewEngine(spend.DefaultTable())
	h, err := NewAnalyticsHandlers(store, analytics.New(store, spendEng), spendEng, waste.Config{})
	if err != nil {
		t.Fatal(err)
	}
	srv := New("127.0.0.1:0", WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second), WithAnalytics(h.WithEvents(events)))
	ctx, cancel := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		stop, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(stop)
	})
	waitListening(t, srv.Addr())
	return "http://" + srv.Addr()
}

// unattributedID is a prompt event with no workflow, older than the
// seeded ones.
const unattributedID = "unattributed"

// addUnattributed stores unattributedID, so that a part of the spend is
// not attributed.
func addUnattributed(t *testing.T, store *sqlite.Store) {
	t.Helper()
	if err := store.Append(context.Background(), &eventschema.Envelope{
		ID: unattributedID, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: time.Now().UTC().Add(-4 * time.Hour), Source: "test",
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o-mini",
			InputTokens: 1000, OutputTokens: 200, TotalTokens: 1200, CostUSD: 0.40, Status: 200},
	}); err != nil {
		t.Fatal(err)
	}
}

// dropBehindTheCache deletes the first event stored without the change log
// seeing it: an answer that still counts it came from a cache. (The
// newest is the one a cache checks is still there.)
func dropBehindTheCache(t *testing.T, store *sqlite.Store) {
	t.Helper()
	for _, q := range []string{`DROP TRIGGER events_changes_on_delete`, `DELETE FROM events WHERE rowid = (SELECT MIN(rowid) FROM events)`} {
		if _, err := store.DB().ExecContext(context.Background(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// The scorecard reads the period and its baseline through the daemon's
// cache of events: from the store, each one decoded five weeks of them.
func TestScorecardReadsEventsThroughTheDaemonsCache(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	for _, cached := range []bool{true, false} {
		t.Run(fmt.Sprintf("cached=%v", cached), func(t *testing.T) {
			store, done := seedAnalyticsStore(t)
			defer done()
			addUnattributed(t, store)
			var events *sqlite.EventCache
			if cached {
				events = sqlite.NewEventCache(store, 36*24*time.Hour, 0)
			}
			base := scorecardServer(t, store, events)
			sac := func() any {
				card := getJSON(t, base+"/api/scorecard?since_days=7")
				kpi, _ := card["spend_attribution_completeness"].(map[string]any)
				return kpi["value"]
			}
			before, _ := sac().(float64)
			if before <= 0 || before >= 100 {
				t.Fatalf("SAC %v, want part of the spend attributed", before)
			}
			dropBehindTheCache(t, store)
			got := sac()
			if cached && got != before {
				t.Errorf("SAC %v once the event was gone from the store, want %v from the cache", got, before)
			}
			if !cached && got == before {
				t.Errorf("SAC %v without a cache, as before the event was gone from the store", got)
			}
		})
	}
}

// The handlers keep what the scorecard read of the transcripts: the next
// one opens no transcript that did not change.
func TestScorecardKeepsTheTranscriptsItRead(t *testing.T) {
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	path := filepath.Join(claude, "projects", "proj", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := func(text string) string {
		return fmt.Sprintf(`{"type":"user","timestamp":%q,"sessionId":"s","message":{"content":%q}}`+"\n", at, text)
	}
	if err := os.WriteFile(path, []byte(line("refactor the retry path")+line("ok")), 0o600); err != nil {
		t.Fatal(err)
	}
	store, done := seedAnalyticsStore(t)
	defer done()
	base := scorecardServer(t, store, nil)
	cgr := func() any {
		card := getJSON(t, base+"/api/scorecard")
		kpi, _ := card["confirmation_gate_rate"].(map[string]any)
		return kpi["value"]
	}
	if got := cgr(); got != 50.0 {
		t.Fatalf("CGR %v, want 50", got)
	}
	// Unreadable now: only what was kept can answer.
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if f, err := os.Open(path); err == nil {
		_ = f.Close()
		t.Skip("the transcript stays readable (running as root?)")
	}
	if got := cgr(); got != 50.0 {
		t.Errorf("CGR %v on the second scorecard, want 50 from what the first read", got)
	}
}
