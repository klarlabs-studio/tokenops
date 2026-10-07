package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
)

// `routing proposals` gives the answer tokenops_routing gives: the
// question to put to the operator, and whether the upgrade's price could
// be checked. It printed the raw log records, without either.
func TestRoutingProposalsMatchTheMCPAnswer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := routingapproval.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Propose(routingapproval.Record{Key: "anthropic|sonnet|opus", Provider: "anthropic",
		From: "claude-sonnet-5", To: "claude-opus-5", Preferred: "claude-sonnet-5", Reason: "hard task"}); err != nil {
		t.Fatal(err)
	}
	out, err := executeRoot(t, "routing", "proposals", "--store", path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "claude-sonnet-5 -> claude-opus-5") || !strings.Contains(out, "unverifiable") {
		t.Errorf("text output:\n%s", out)
	}
	out, err = executeRoot(t, "routing", "proposals", "--store", path, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Pending []struct {
			Key      string `json:"key"`
			Question string `json:"question"`
			Pricing  string `json:"pricing"`
		} `json:"pending"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if len(got.Pending) != 1 || got.Pending[0].Question == "" || got.Pending[0].Pricing == "" {
		t.Errorf("json %+v", got)
	}
}
