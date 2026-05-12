package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// fakeBus captures every published envelope so tests can assert on
// what the tracker emits without booting the sqlite store.
type fakeBus struct {
	mu   sync.Mutex
	envs []*eventschema.Envelope
}

func (b *fakeBus) Publish(env *eventschema.Envelope) {
	b.mu.Lock()
	b.envs = append(b.envs, env)
	b.mu.Unlock()
}

func (b *fakeBus) PublishedCount() int64       { return int64(len(b.envs)) }
func (b *fakeBus) DroppedCount() int64         { return 0 }
func (b *fakeBus) Close(_ time.Duration) error { return nil }

func TestRecordEmitsPlanIncludedEvent(t *testing.T) {
	bus := &fakeBus{}
	tr := New(bus, Options{Provider: eventschema.ProviderAnthropic})
	tr.Record(context.Background(), Options{
		Provider:    eventschema.ProviderAnthropic,
		SourceLabel: "mcp-session",
	}, "tokenops_spend_summary")

	if len(bus.envs) != 1 {
		t.Fatalf("publish count = %d, want 1", len(bus.envs))
	}
	env := bus.envs[0]
	if env.Type != eventschema.EventTypePrompt {
		t.Errorf("Type = %s, want prompt", env.Type)
	}
	if env.Source != "mcp-session" {
		t.Errorf("Source = %q, want mcp-session", env.Source)
	}
	if env.Attributes["mcp.tool"] != "tokenops_spend_summary" {
		t.Errorf("attribute mcp.tool = %q", env.Attributes["mcp.tool"])
	}
	p, ok := env.Payload.(*eventschema.PromptEvent)
	if !ok {
		t.Fatalf("payload type %T", env.Payload)
	}
	if p.CostSource != eventschema.CostSourcePlanIncluded {
		t.Errorf("CostSource = %q, want plan_included", p.CostSource)
	}
	if p.Provider != eventschema.ProviderAnthropic {
		t.Errorf("Provider = %q, want anthropic", p.Provider)
	}
}

func TestRecordCountsPerTool(t *testing.T) {
	bus := &fakeBus{}
	tr := New(bus, Options{Provider: eventschema.ProviderAnthropic})
	for i := 0; i < 3; i++ {
		tr.Record(context.Background(), Options{Provider: eventschema.ProviderAnthropic}, "tokenops_spend_summary")
	}
	tr.Record(context.Background(), Options{Provider: eventschema.ProviderAnthropic}, "tokenops_top_consumers")

	counts := tr.Counts()
	if counts["tokenops_spend_summary"] != 3 {
		t.Errorf("spend_summary count = %d, want 3", counts["tokenops_spend_summary"])
	}
	if counts["tokenops_top_consumers"] != 1 {
		t.Errorf("top_consumers count = %d, want 1", counts["tokenops_top_consumers"])
	}
}

func TestRecordNilBusStillCounts(t *testing.T) {
	tr := New(nil, Options{Provider: eventschema.ProviderAnthropic})
	tr.Record(context.Background(), Options{Provider: eventschema.ProviderAnthropic}, "tokenops_spend_summary")
	if tr.Counts()["tokenops_spend_summary"] != 1 {
		t.Errorf("expected counter to advance even without bus")
	}
}
