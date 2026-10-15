package opencode

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"

	opencodestore "go.klarlabs.de/tokenops/internal/infra/opencodedb"
)

// An operator on opencode 2.0.29 has a store with only opencode 2's tables:
// session_v2 and session_message, and no `message` or `session` table at
// all. A daemon from before opencode 2 support logged "no such table:
// message" every 30s against it. Every assistant turn in such a store must
// arrive as a prompt event under the provider that served it.
func TestScanReadsAPureOpencode2Store(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE session_v2 (id TEXT PRIMARY KEY, directory TEXT NOT NULL, time_created INTEGER, time_updated INTEGER)`,
		`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`INSERT INTO session_v2 VALUES ('s1', '/work/api', 1791000000000, 1791000600000)`,
		`INSERT INTO session_message VALUES ('u1', 's1', 'user', 1, 1791000000000, 1791000000000, '{"text":"add the endpoint"}')`,
		`INSERT INTO session_message VALUES ('a1', 's1', 'assistant', 2, 1791000060000, 1791000060000,
			'{"model":{"providerID":"anthropic","id":"claude-sonnet-4-5"},"cost":0,"tokens":{"input":1200,"output":300,"reasoning":0,"cache":{"read":800,"write":100}}}')`,
		`INSERT INTO session_message VALUES ('a2', 's1', 'assistant', 3, 1791000120000, 1791000120000,
			'{"model":{"providerID":"openai","id":"gpt-5"},"cost":0,"tokens":{"input":500,"output":50,"reasoning":10,"cache":{"read":0,"write":0}}}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = db.Close()

	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{Root: path, Store: opencodestore.Store{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p.scan(context.Background(), path)

	if len(bus.envelopes) != 2 {
		t.Fatalf("published %d events from a pure opencode 2 store, want the 2 assistant turns", len(bus.envelopes))
	}
	byID := map[string]*eventschema.PromptEvent{}
	for _, env := range bus.envelopes {
		if env.Source != SourceTag {
			t.Errorf("source %q, want %q", env.Source, SourceTag)
		}
		pe, ok := env.Payload.(*eventschema.PromptEvent)
		if !ok {
			t.Fatalf("payload %T, want a prompt event", env.Payload)
		}
		byID[env.Attributes["message_id"]] = pe
		if env.Attributes["project"] == "" {
			t.Errorf("turn %s has no project; session_v2's directory should name it", env.Attributes["message_id"])
		}
	}
	// The input count includes the cache reads and writes, as it does for
	// opencode 1.x turns.
	a1 := byID["a1"]
	if a1 == nil || a1.Provider != eventschema.ProviderAnthropic || a1.RequestModel != "claude-sonnet-4-5" ||
		a1.InputTokens != 1200+800+100 || a1.OutputTokens != 300 || a1.CachedInputTokens != 800 || a1.CacheWriteInputTokens != 100 {
		t.Errorf("a1 = %+v", a1)
	}
	if a2 := byID["a2"]; a2 == nil || a2.Provider != eventschema.ProviderOpenAI || a2.RequestModel != "gpt-5" {
		t.Errorf("a2 = %+v", a2)
	}
}
