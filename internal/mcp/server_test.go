package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// runRequest serialises req as one JSON-RPC line, runs the server until
// the input stream EOFs, and returns the parsed response. EOF on the
// input is the deterministic signal — no busy-wait, no race on the
// output buffer.
func runRequest(t *testing.T, srv *Server, req map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(req)
	in := bytes.NewReader(append(body, '\n'))
	out := &bytes.Buffer{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Serve(ctx, in, out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if out.Len() == 0 {
		t.Fatalf("no response on stdout")
	}
	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("not json: %v\n%s", err, out.String())
	}
	return resp
}

func TestInitializeReturnsCapabilities(t *testing.T) {
	srv := NewServer("tokenops", "test", nil)
	resp := runRequest(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("missing result: %v", resp)
	}
	if result["protocolVersion"] != ProtocolVersion {
		t.Errorf("protocol version = %v", result["protocolVersion"])
	}
	caps, _ := result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("missing tools capability: %v", caps)
	}
}

func TestToolsListReturnsRegisteredTool(t *testing.T) {
	srv := NewServer("tokenops", "test", nil)
	srv.AddTool(&Tool{
		Name:        "demo",
		Description: "demo tool",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			return "ok", nil
		},
	})
	resp := runRequest(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", tools)
	}
	first, _ := tools[0].(map[string]any)
	if first["name"] != "demo" {
		t.Errorf("name = %v", first["name"])
	}
}

func TestToolsCallInvokesHandler(t *testing.T) {
	srv := NewServer("tokenops", "test", nil)
	var got atomic.Int64
	srv.AddTool(&Tool{
		Name:        "echo",
		Description: "echo argument",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			got.Add(1)
			return string(args), nil
		},
	})
	resp := runRequest(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "echo",
			"arguments": map[string]any{"x": 1},
		},
	})
	if got.Load() != 1 {
		t.Errorf("handler not invoked")
	}
	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %v", content)
	}
	first, _ := content[0].(map[string]any)
	if !strings.Contains(first["text"].(string), `"x":1`) {
		t.Errorf("echoed text = %v", first["text"])
	}
}

func TestToolsCallUnknownTool(t *testing.T) {
	srv := NewServer("tokenops", "test", nil)
	resp := runRequest(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "tools/call",
		"params":  map[string]any{"name": "missing"},
	})
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected error response, got %v", resp)
	}
	if !strings.Contains(errObj["message"].(string), "tool not found") {
		t.Errorf("error = %v", errObj)
	}
}

func TestPingReturnsEmpty(t *testing.T) {
	srv := NewServer("tokenops", "test", nil)
	resp := runRequest(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "ping",
	})
	if _, ok := resp["result"]; !ok {
		t.Errorf("ping missing result: %v", resp)
	}
}

func TestServeIgnoresNotifications(t *testing.T) {
	srv := NewServer("tokenops", "test", nil)
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	in.WriteString(`{"jsonrpc":"2.0","method":"notify"}` + "\n")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = srv.Serve(ctx, in, out)
	if out.Len() != 0 {
		t.Errorf("notifications must not produce output, got %s", out.String())
	}
}

// io.Discard import sanity check.
var _ = io.Discard
