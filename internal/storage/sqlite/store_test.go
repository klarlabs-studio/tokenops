package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustPromptEnvelope(t *testing.T, id string, ts time.Time, p *eventschema.PromptEvent) *eventschema.Envelope {
	t.Helper()
	return &eventschema.Envelope{
		ID:            id,
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     ts,
		Source:        "proxy",
		Payload:       p,
	}
}

func TestOpenAppliesMigrationsIdempotently(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.db")

	s1, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	// Re-opening must succeed without re-applying migrations and without
	// any "table already exists" errors.
	s2, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s2.Close()

	var n int
	if err := s2.DB().QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if n != len(migrations) {
		t.Fatalf("schema_migrations rows = %d, want %d", n, len(migrations))
	}
}

func TestAppendAndQueryRoundTripPrompt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Date(2026, 5, 9, 10, 30, 0, 0, time.UTC)
	env := mustPromptEnvelope(t, "01H8XYZ-PROMPT-1", now, &eventschema.PromptEvent{
		PromptHash:    "sha256:abc",
		Provider:      eventschema.ProviderOpenAI,
		RequestModel:  "gpt-4o-mini",
		ResponseModel: "gpt-4o-mini-2026-01",
		InputTokens:   100,
		OutputTokens:  50,
		TotalTokens:   150,
		ContextSize:   80,
		Latency:       250 * time.Millisecond,
		Streaming:     false,
		Status:        200,
		FinishReason:  "stop",
		CostUSD:       0.000345,
		WorkflowID:    "wf-1",
		AgentID:       "agent-a",
		SessionID:     "sess-1",
		UserID:        "user-1",
	})
	env.Attributes = map[string]string{"deploy": "test"}

	if err := s.Append(ctx, env); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := s.Query(ctx, Filter{Type: eventschema.EventTypePrompt})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	g := got[0]
	if g.ID != env.ID {
		t.Errorf("id = %q, want %q", g.ID, env.ID)
	}
	if !g.Timestamp.Equal(env.Timestamp) {
		t.Errorf("timestamp = %s, want %s", g.Timestamp, env.Timestamp)
	}
	if g.Source != "proxy" {
		t.Errorf("source = %q, want proxy", g.Source)
	}
	if g.Attributes["deploy"] != "test" {
		t.Errorf("attributes lost: %v", g.Attributes)
	}
	gp, ok := g.Payload.(*eventschema.PromptEvent)
	if !ok {
		t.Fatalf("payload type = %T, want *PromptEvent", g.Payload)
	}
	if gp.RequestModel != "gpt-4o-mini" || gp.TotalTokens != 150 || gp.WorkflowID != "wf-1" {
		t.Errorf("payload mismatch: %+v", gp)
	}
}

func TestAppendBatchAtomic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 5, 9, 10, 30, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mustPromptEnvelope(t, "p-1", base, &eventschema.PromptEvent{
			PromptHash: "h1", Provider: eventschema.ProviderAnthropic,
			RequestModel: "claude-sonnet-4-6", InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
		}),
		mustPromptEnvelope(t, "p-2", base.Add(time.Second), &eventschema.PromptEvent{
			PromptHash: "h2", Provider: eventschema.ProviderAnthropic,
			RequestModel: "claude-sonnet-4-6", InputTokens: 20, OutputTokens: 8, TotalTokens: 28,
		}),
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("batch: %v", err)
	}
	n, err := s.Count(ctx, Filter{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
}

func TestAppendIsIdempotentOnConflict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	env := mustPromptEnvelope(t, "dup-1", time.Now().UTC(), &eventschema.PromptEvent{
		PromptHash: "h", Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o",
	})
	if err := s.Append(ctx, env); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := s.Append(ctx, env); err != nil {
		t.Fatalf("second append: %v", err)
	}
	n, err := s.Count(ctx, Filter{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("count = %d, want 1 (ON CONFLICT DO NOTHING)", n)
	}
}

func TestQueryFilters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	envs := []*eventschema.Envelope{
		mustPromptEnvelope(t, "a-1", base, &eventschema.PromptEvent{
			Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o",
			WorkflowID: "wf-A", AgentID: "agent-1", SessionID: "s-A",
		}),
		mustPromptEnvelope(t, "a-2", base.Add(1*time.Hour), &eventschema.PromptEvent{
			Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o-mini",
			WorkflowID: "wf-A", AgentID: "agent-1", SessionID: "s-A",
		}),
		mustPromptEnvelope(t, "b-1", base.Add(2*time.Hour), &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-4-6",
			WorkflowID: "wf-B", AgentID: "agent-2", SessionID: "s-B",
		}),
		{
			ID: "wf-evt-1", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypeWorkflow, Timestamp: base.Add(3 * time.Hour),
			Source: "proxy",
			Payload: &eventschema.WorkflowEvent{
				WorkflowID: "wf-A", State: eventschema.WorkflowStateCompleted,
				StepCount: 2, CumulativeTotalTokens: 300,
			},
		},
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("batch: %v", err)
	}

	t.Run("by workflow", func(t *testing.T) {
		got, err := s.Query(ctx, Filter{WorkflowID: "wf-A"})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("len = %d, want 3", len(got))
		}
	})

	t.Run("by provider+model", func(t *testing.T) {
		got, err := s.Query(ctx, Filter{Provider: "openai", Model: "gpt-4o-mini"})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if len(got) != 1 || got[0].ID != "a-2" {
			t.Fatalf("unexpected results: %+v", got)
		}
	})

	t.Run("by type", func(t *testing.T) {
		got, err := s.Query(ctx, Filter{Type: eventschema.EventTypeWorkflow})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if _, ok := got[0].Payload.(*eventschema.WorkflowEvent); !ok {
			t.Fatalf("payload type = %T", got[0].Payload)
		}
	})

	t.Run("by time window", func(t *testing.T) {
		got, err := s.Query(ctx, Filter{
			Since: base.Add(30 * time.Minute),
			Until: base.Add(150 * time.Minute),
		})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
	})

	t.Run("limit", func(t *testing.T) {
		got, err := s.Query(ctx, Filter{Limit: 2})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
	})
}

func TestRoundTripAllEventTypes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 9, 11, 0, 0, 0, time.UTC)

	envs := []*eventschema.Envelope{
		{
			ID: "p", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: now,
			Payload: &eventschema.PromptEvent{
				PromptHash: "h", Provider: eventschema.ProviderGemini,
				RequestModel: "gemini-2.5", InputTokens: 1, OutputTokens: 2, TotalTokens: 3,
			},
		},
		{
			ID: "w", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypeWorkflow, Timestamp: now.Add(time.Second),
			Payload: &eventschema.WorkflowEvent{
				WorkflowID: "wf", State: eventschema.WorkflowStateProgress,
				StepCount: 1, CumulativeTotalTokens: 3,
			},
		},
		{
			ID: "o", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypeOptimization, Timestamp: now.Add(2 * time.Second),
			Payload: &eventschema.OptimizationEvent{
				PromptHash:             "h",
				Kind:                   eventschema.OptimizationTypePromptCompress,
				Mode:                   eventschema.OptimizationModePassive,
				EstimatedSavingsTokens: 10,
				QualityScore:           0.95,
				Decision:               eventschema.OptimizationDecisionApplied,
				WorkflowID:             "wf",
			},
		},
		{
			ID: "c", SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypeCoaching, Timestamp: now.Add(3 * time.Second),
			Payload: &eventschema.CoachingEvent{
				SessionID: "sess", WorkflowID: "wf",
				Kind:                   eventschema.CoachingKindReducePromptSize,
				Summary:                "Trim system prompt",
				EstimatedSavingsTokens: 25,
				EfficiencyScore:        0.7,
			},
		},
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatalf("batch: %v", err)
	}

	got, err := s.Query(ctx, Filter{Limit: 100})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	wantTypes := []eventschema.EventType{
		eventschema.EventTypePrompt,
		eventschema.EventTypeWorkflow,
		eventschema.EventTypeOptimization,
		eventschema.EventTypeCoaching,
	}
	for i, env := range got {
		if env.Type != wantTypes[i] {
			t.Errorf("envelope %d: type = %q, want %q", i, env.Type, wantTypes[i])
		}
		if env.Type != env.Payload.Type() {
			t.Errorf("envelope %d: payload type %q != envelope type %q",
				i, env.Payload.Type(), env.Type)
		}
	}
}

func TestAppendBatchEmptyIsNoop(t *testing.T) {
	s := newTestStore(t)
	if err := s.AppendBatch(context.Background(), nil); err != nil {
		t.Fatalf("nil batch: %v", err)
	}
	if err := s.AppendBatch(context.Background(), []*eventschema.Envelope{}); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
}

func TestAppendRejectsInvalidEnvelope(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	cases := []struct {
		name string
		env  *eventschema.Envelope
	}{
		{"nil envelope", nil},
		{"empty id", &eventschema.Envelope{
			SchemaVersion: eventschema.SchemaVersion,
			Type:          eventschema.EventTypePrompt,
			Timestamp:     time.Now().UTC(),
			Payload:       &eventschema.PromptEvent{},
		}},
		{"type mismatch", &eventschema.Envelope{
			ID:            "x",
			SchemaVersion: eventschema.SchemaVersion,
			Type:          eventschema.EventTypePrompt,
			Timestamp:     time.Now().UTC(),
			Payload:       &eventschema.WorkflowEvent{},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.Append(ctx, tc.env); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestOpenInvalidPath(t *testing.T) {
	if _, err := Open(context.Background(), "", Options{}); err == nil {
		t.Fatal("empty path: expected error")
	}
}
