package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.klarlabs.de/mcp/protocol"

	"go.klarlabs.de/tokenops/internal/contexts/spend/session"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestSessionMiddlewareRecordsToolsCall(t *testing.T) {
	tr := session.New(nil, session.Options{Provider: eventschema.ProviderAnthropic})
	mw := SessionMiddleware(tr, func() eventschema.Provider { return eventschema.ProviderAnthropic })
	called := false
	next := func(_ context.Context, _ *protocol.Request) (*protocol.Response, error) {
		called = true
		return nil, nil
	}

	params, _ := json.Marshal(map[string]string{"name": "tokenops_spend_summary"})
	req := &protocol.Request{Method: "tools/call", Params: params}
	if _, err := mw(next)(context.Background(), req); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if !called {
		t.Error("downstream handler must still run")
	}
	if tr.Counts()["tokenops_spend_summary"] != 1 {
		t.Errorf("counts=%v want spend_summary=1", tr.Counts())
	}
}

func TestSessionMiddlewareSkipsNonToolsCall(t *testing.T) {
	tr := session.New(nil, session.Options{Provider: eventschema.ProviderAnthropic})
	mw := SessionMiddleware(tr, func() eventschema.Provider { return eventschema.ProviderAnthropic })
	next := func(_ context.Context, _ *protocol.Request) (*protocol.Response, error) {
		return nil, nil
	}
	req := &protocol.Request{Method: "initialize", Params: json.RawMessage(`{}`)}
	if _, err := mw(next)(context.Background(), req); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if len(tr.Counts()) != 0 {
		t.Errorf("initialize must not record a tool call, got %v", tr.Counts())
	}
}

func TestSessionMiddlewareNilTrackerIsPassThrough(t *testing.T) {
	mw := SessionMiddleware(nil, func() eventschema.Provider { return eventschema.ProviderAnthropic })
	called := false
	next := func(_ context.Context, _ *protocol.Request) (*protocol.Response, error) {
		called = true
		return nil, nil
	}
	params, _ := json.Marshal(map[string]string{"name": "anything"})
	req := &protocol.Request{Method: "tools/call", Params: params}
	if _, err := mw(next)(context.Background(), req); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if !called {
		t.Error("nil tracker must still call downstream")
	}
}

type pingBus struct{ envs []*eventschema.Envelope }

func (b *pingBus) Publish(env *eventschema.Envelope) { b.envs = append(b.envs, env) }
func (b *pingBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.Publish(env)
	return nil
}
func (b *pingBus) PublishedCount() int64       { return int64(len(b.envs)) }
func (b *pingBus) DroppedCount() int64         { return 0 }
func (b *pingBus) Close(_ time.Duration) error { return nil }

// A plan bound after the server started must attribute the pings that
// follow it: the provider used to be read once, at startup, so every ping
// stayed "unknown" until the client restarted the server.
func TestSessionMiddlewareStampsTheProviderCurrentAtEachCall(t *testing.T) {
	bus := &pingBus{}
	tr := session.New(bus, session.Options{})
	current := eventschema.ProviderUnknown
	mw := SessionMiddleware(tr, func() eventschema.Provider { return current })
	next := func(_ context.Context, _ *protocol.Request) (*protocol.Response, error) { return nil, nil }
	params, _ := json.Marshal(map[string]string{"name": "tokenops_plan_headroom"})
	call := func() {
		if _, err := mw(next)(context.Background(), &protocol.Request{Method: "tools/call", Params: params}); err != nil {
			t.Fatal(err)
		}
	}
	call()
	current = eventschema.ProviderAnthropic // e.g. tokenops_plan_set anthropic ...
	call()
	if len(bus.envs) != 2 {
		t.Fatalf("recorded %d pings, want 2", len(bus.envs))
	}
	got := bus.envs[1].Payload.(*eventschema.PromptEvent).Provider
	if got != eventschema.ProviderAnthropic {
		t.Errorf("ping after the plan was bound stamped %q, want anthropic", got)
	}
}
