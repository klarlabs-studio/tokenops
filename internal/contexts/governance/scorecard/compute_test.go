package scorecard

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func openTempStore(t *testing.T) *sqlite.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	store, err := sqlite.Open(context.Background(), path, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func appendPrompts(t *testing.T, store *sqlite.Store, events ...*eventschema.PromptEvent) {
	t.Helper()
	now := time.Now().UTC()
	envs := make([]*eventschema.Envelope, 0, len(events))
	for i, pe := range events {
		envs = append(envs, &eventschema.Envelope{
			ID:            "p" + filepath.Base(t.TempDir()) + itoa(i),
			SchemaVersion: eventschema.SchemaVersion,
			Type:          eventschema.EventTypePrompt,
			Timestamp:     now.Add(time.Duration(i) * time.Millisecond),
			Payload:       pe,
		})
	}
	if err := store.AppendBatch(context.Background(), envs); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func appendOpt(t *testing.T, store *sqlite.Store, events ...*eventschema.OptimizationEvent) {
	t.Helper()
	now := time.Now().UTC()
	envs := make([]*eventschema.Envelope, 0, len(events))
	for i, oe := range events {
		envs = append(envs, &eventschema.Envelope{
			ID:            "o" + filepath.Base(t.TempDir()) + itoa(i),
			SchemaVersion: eventschema.SchemaVersion,
			Type:          eventschema.EventTypeOptimization,
			Timestamp:     now.Add(time.Duration(i) * time.Millisecond),
			Payload:       oe,
		})
	}
	if err := store.AppendBatch(context.Background(), envs); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestComputeFVTMedianPerSession(t *testing.T) {
	store := openTempStore(t)
	appendPrompts(t, store,
		&eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: "m", SessionID: "a", InputTokens: 10, OutputTokens: 5, TotalTokens: 15, Latency: 1 * time.Second},
		&eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: "m", SessionID: "b", InputTokens: 20, OutputTokens: 5, TotalTokens: 25, Latency: 3 * time.Second},
		&eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: "m", SessionID: "c", InputTokens: 30, OutputTokens: 5, TotalTokens: 35, Latency: 5 * time.Second},
		// Second event in session a — should not affect median.
		&eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: "m", SessionID: "a", InputTokens: 10, OutputTokens: 5, TotalTokens: 15, Latency: 60 * time.Second},
	)
	kpis, err := Compute(context.Background(), sqliteReader{store: store}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !kpis.FVTComputed {
		t.Fatal("expected FVT computed")
	}
	if kpis.FVTSeconds < 2.5 || kpis.FVTSeconds > 3.5 {
		t.Errorf("FVT median = %f, want ~3", kpis.FVTSeconds)
	}
}

func TestComputeTEU(t *testing.T) {
	store := openTempStore(t)
	appendPrompts(t, store,
		&eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: "m", SessionID: "s", InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100},
		&eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: "m", SessionID: "s", InputTokens: 4000, OutputTokens: 200, TotalTokens: 4200},
	)
	appendOpt(t, store,
		&eventschema.OptimizationEvent{PromptHash: "h", Kind: eventschema.OptimizationTypePromptCompress, Mode: eventschema.OptimizationModePassive, Decision: eventschema.OptimizationDecisionApplied, EstimatedSavingsTokens: 500},
		&eventschema.OptimizationEvent{PromptHash: "h", Kind: eventschema.OptimizationTypeDedupe, Mode: eventschema.OptimizationModePassive, Decision: eventschema.OptimizationDecisionApplied, EstimatedSavingsTokens: 500},
	)
	kpis, err := Compute(context.Background(), sqliteReader{store: store}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !kpis.TEUComputed {
		t.Fatal("expected TEU computed")
	}
	if kpis.TokenEfficiency < 19 || kpis.TokenEfficiency > 21 {
		t.Errorf("TEU = %f, want 20", kpis.TokenEfficiency)
	}
}

func TestComputeSAC(t *testing.T) {
	store := openTempStore(t)
	appendPrompts(t, store,
		&eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: "m", WorkflowID: "wf-1", InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		&eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: "m", AgentID: "a-1", InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		&eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: "m", InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
		&eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI, RequestModel: "m", SessionID: "s-1", InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
	)
	kpis, err := Compute(context.Background(), sqliteReader{store: store}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !kpis.SACComputed {
		t.Fatal("expected SAC computed")
	}
	if kpis.SpendAttribution < 70 || kpis.SpendAttribution > 80 {
		t.Errorf("SAC = %f, want 75", kpis.SpendAttribution)
	}
}

func TestComputeEmptyStoreFlagsUnComputed(t *testing.T) {
	store := openTempStore(t)
	kpis, err := Compute(context.Background(), sqliteReader{store: store}, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if kpis.FVTComputed || kpis.TEUComputed || kpis.SACComputed {
		t.Errorf("expected all flags false on empty store, got %+v", kpis)
	}
}
