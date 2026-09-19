package opencode

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type captureBus struct {
	mu        sync.Mutex
	envelopes []*eventschema.Envelope
}

func (b *captureBus) Publish(env *eventschema.Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.envelopes = append(b.envelopes, env)
}

func (b *captureBus) PublishedCount() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int64(len(b.envelopes))
}
func (b *captureBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.Publish(env)
	return nil
}
func (b *captureBus) DroppedCount() int64         { return 0 }
func (b *captureBus) Close(_ time.Duration) error { return nil }

// An opencode database that has not changed since the last scan holds no
// new turns, so it is not read again. Reading every message row each 30s
// kept a 1.3 GB database untouched since July on the hot path.
func TestScanSkipsAnUnchangedDatabase(t *testing.T) {
	path := newTestDB(t, map[string]string{"m1": `{"role":"assistant","time":{"created":1771056613283},"modelID":"claude-opus-4.6","providerID":"anthropic","tokens":{"input":10,"output":20,"reasoning":0,"cache":{"read":0,"write":0}}}`})
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{Root: path, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	p.scan(context.Background(), path)
	p.scan(context.Background(), path)
	if p.dbReads != 1 {
		t.Fatalf("read the database %d times across two scans with no change, want 1", p.dbReads)
	}

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO message (id, session_id, data) VALUES (?, ?, ?)`, "m2", "sess-1",
		`{"role":"assistant","time":{"created":1771056699000},"modelID":"claude-opus-4.6","providerID":"anthropic","tokens":{"input":5,"output":5,"reasoning":0,"cache":{"read":0,"write":0}}}`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	p.scan(context.Background(), path)
	if p.dbReads != 2 {
		t.Fatalf("read the database %d times after a write, want 2", p.dbReads)
	}
	if got := bus.PublishedCount(); got != 2 {
		t.Errorf("published %d turns, want 2 — the row written after the first scan must still arrive", got)
	}
}
