package coaching

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/coaching/waste"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/replay"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := sqlite.Open(context.Background(), path, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mkPrompt(id, wf string, ts time.Time, tokens int64) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: id, SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypePrompt, Timestamp: ts, Source: "test",
		Payload: &eventschema.PromptEvent{
			PromptHash: "h-" + id, Provider: eventschema.ProviderOpenAI,
			RequestModel: "gpt-4o", InputTokens: tokens, OutputTokens: 50,
			TotalTokens: tokens + 50, WorkflowID: wf, AgentID: "agent-A",
		},
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type recordingSink struct {
	mu    sync.Mutex
	envs  []*eventschema.Envelope
	count atomic.Int64
}

func (r *recordingSink) Publish(env *eventschema.Envelope) {
	r.mu.Lock()
	r.envs = append(r.envs, env)
	r.mu.Unlock()
	r.count.Add(1)
}

func startPipelineForTest(t *testing.T, cfg Config, store *sqlite.Store, sink Sink) *Pipeline {
	t.Helper()
	rep := replay.New(store, optimizer.NewPipeline(), nil)
	det := waste.New(waste.Config{MaxContextTokens: 100, ContextGrowthLimitTokens: 100, MaxConsecutiveAgentLoops: 2})
	p := New(cfg, rep, det, sink)
	go func() { _ = p.Start(context.Background()) }()
	t.Cleanup(func() {
		p.Close()
		p.Wait()
	})
	return p
}

func TestPipelineEmitsCoachingEvents(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mkPrompt("a", "wf-1", base, 200), // exceeds MaxContextTokens=100
		mkPrompt("b", "wf-1", base.Add(time.Minute), 300),
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("append: %v", err)
	}
	sink := &recordingSink{}
	p := startPipelineForTest(t, Config{Concurrency: 1, Logger: discardLogger()}, store, sink)
	if err := p.Submit(ctx, Job{WorkflowID: "wf-1"}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && sink.count.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if sink.count.Load() == 0 {
		t.Fatalf("no coaching events: stats=%s", p.Stats())
	}
	for _, env := range sink.envs {
		if env.Type != eventschema.EventTypeCoaching {
			t.Errorf("wrong type: %s", env.Type)
		}
	}
}

func TestPipelineCostBudgetSkips(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	_ = store.Append(ctx, mkPrompt("a", "wf-1", time.Now().UTC(), 200))
	sink := &recordingSink{}
	p := startPipelineForTest(t, Config{
		Concurrency: 1, CostBudgetUSD: 1.0, Logger: discardLogger(),
	}, store, sink)
	if err := p.Submit(ctx, Job{WorkflowID: "wf-1", CostEstimateUSD: 0.6}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := p.Submit(ctx, Job{WorkflowID: "wf-1", CostEstimateUSD: 0.6}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if p.Stats().Processed >= 1 && p.Stats().Skipped >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stats := p.Stats()
	if stats.Skipped == 0 {
		t.Errorf("expected at least one skip from budget exhaustion: %s", stats)
	}
}

func TestPipelineEmptySessionIgnored(t *testing.T) {
	store := newStore(t)
	sink := &recordingSink{}
	p := startPipelineForTest(t, Config{Concurrency: 1, Logger: discardLogger()}, store, sink)
	if err := p.Submit(context.Background(), Job{WorkflowID: "missing"}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if sink.count.Load() != 0 {
		t.Errorf("empty session should not emit: %d", sink.count.Load())
	}
}

func TestStartReturnsErrInvalidConfig(t *testing.T) {
	p := New(Config{Logger: discardLogger()}, nil, nil, nil)
	if err := p.Start(context.Background()); err != ErrInvalidConfig {
		t.Errorf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestSubmitAfterCloseReturnsErrClosed(t *testing.T) {
	store := newStore(t)
	sink := &recordingSink{}
	p := New(Config{Concurrency: 1, Logger: discardLogger()},
		replay.New(store, optimizer.NewPipeline(), nil),
		waste.New(waste.Config{}),
		sink)
	go func() { _ = p.Start(context.Background()) }()
	p.Close()
	p.Wait()
	if err := p.Submit(context.Background(), Job{WorkflowID: "x"}); err != ErrClosed {
		t.Errorf("err = %v, want ErrClosed", err)
	}
}

func TestStatsString(t *testing.T) {
	s := Stats{Processed: 3, Skipped: 1, Emitted: 7, CostSpent: 1.234}
	str := s.String()
	if str == "" {
		t.Error("empty string")
	}
}

func TestFuncSinkPublish(t *testing.T) {
	called := false
	s := FuncSink(func(*eventschema.Envelope) { called = true })
	s.Publish(&eventschema.Envelope{})
	if !called {
		t.Error("FuncSink did not call wrapped func")
	}
}
