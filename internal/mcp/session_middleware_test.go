package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/felixgeelhaar/mcp-go/protocol"

	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/session"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func TestSessionMiddlewareRecordsToolsCall(t *testing.T) {
	tr := session.New(nil, session.Options{Provider: eventschema.ProviderAnthropic})
	mw := SessionMiddleware(tr, eventschema.ProviderAnthropic)
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
	mw := SessionMiddleware(tr, eventschema.ProviderAnthropic)
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
	mw := SessionMiddleware(nil, eventschema.ProviderAnthropic)
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
