package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
)

func approvalDeps(t *testing.T) (ApprovalDeps, *routingapproval.Store) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.jsonl")
	st, err := routingapproval.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return ApprovalDeps{StorePath: path, ConfigPath: filepath.Join(dir, "config.yaml")}, st
}

func toolsFor(t *testing.T, d ApprovalDeps) *Server {
	t.Helper()
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := RegisterApprovalTools(srv, d); err != nil {
		t.Fatalf("register: %v", err)
	}
	return srv
}

func callTool(t *testing.T, srv *Server, name string, args any) map[string]any {
	t.Helper()
	raw := execTool(t, srv, name, args)
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%s decode (%q): %v", name, raw, err)
	}
	return out
}

// execToolErr invokes a tool expecting failure.
func execToolErr(t *testing.T, srv *Server, name string, args any) error {
	t.Helper()
	tool, ok := srv.GetTool(name)
	if !ok {
		t.Fatalf("no tool %q registered", name)
	}
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(b))
	return err
}

// A pending proposal must reach the operator as a question, not a status
// line — the whole point is that they get to choose.
func TestProposalsSurfaceAChoice(t *testing.T) {
	d, store := approvalDeps(t)
	if err := store.Propose(routingapproval.Record{
		Key: "anthropic|claude-opus-5|claude-fable-5", Provider: "anthropic",
		From: "claude-opus-5", To: "claude-fable-5", Preferred: "claude-opus-5",
		DeltaUSD: 25, Priced: true, Reason: "costs more",
	}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	got := callTool(t, toolsFor(t, d), "tokenops_routing_proposals", struct{}{})
	pending, _ := got["pending"].([]any)
	if len(pending) != 1 {
		t.Fatalf("pending = %v", got["pending"])
	}
	entry := pending[0].(map[string]any)
	q, _ := entry["question"].(string)
	if !strings.Contains(q, "claude-fable-5") || !strings.Contains(q, "claude-opus-5") {
		t.Errorf("question should name both options: %q", q)
	}
	if entry["preferred_model"] != "claude-opus-5" {
		t.Errorf("preferred_model missing: %+v", entry)
	}
}

// Approving records the proposed model; denying keeps the operator on the
// one they already asked for.
func TestDecideRecordsTheChoice(t *testing.T) {
	for _, tc := range []struct {
		decision, wantModel string
	}{
		{"approve", "claude-fable-5"},
		{"deny", "claude-opus-5"},
	} {
		d, store := approvalDeps(t)
		key := "anthropic|claude-opus-5|claude-fable-5"
		if err := store.Propose(routingapproval.Record{
			Key: key, Provider: "anthropic", From: "claude-opus-5",
			To: "claude-fable-5", Preferred: "claude-opus-5",
		}); err != nil {
			t.Fatalf("propose: %v", err)
		}
		got := callTool(t, toolsFor(t, d), "tokenops_routing_decide",
			routingDecideInput{Key: key, Decision: tc.decision})
		if got["model"] != tc.wantModel {
			t.Errorf("%s -> model = %v, want %v", tc.decision, got["model"], tc.wantModel)
		}
		pending, _ := store.Pending()
		if len(pending) != 0 {
			t.Errorf("%s left the route pending", tc.decision)
		}
	}
}

// Deciding a route nobody proposed is an error, not a silent no-op — it
// almost always means a mistyped key.
func TestDecideUnknownKeyErrors(t *testing.T) {
	d, _ := approvalDeps(t)
	if err := execToolErr(t, toolsFor(t, d), "tokenops_routing_decide",
		routingDecideInput{Key: "nope", Decision: "approve"}); err == nil {
		t.Error("expected an error for an unknown key")
	}
}

// An empty queue says so plainly rather than returning a bare empty list.
func TestNoProposalsReportsClearly(t *testing.T) {
	d, _ := approvalDeps(t)
	got := callTool(t, toolsFor(t, d), "tokenops_routing_proposals", struct{}{})
	if note, _ := got["note"].(string); !strings.Contains(note, "no upgrades") {
		t.Errorf("note = %q", note)
	}
}
