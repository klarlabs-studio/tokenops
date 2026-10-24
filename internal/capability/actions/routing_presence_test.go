package actions

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
)

// With boundary_approval: presence an agent cannot approve a model upgrade
// (MCP), and a terminal approval without a proof is refused with the
// command that signs one; a deny needs nothing. Under channel the old
// behaviour holds.
func TestAnswerRoutingUnderPresence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := routingapproval.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	key := "anthropic|sonnet|opus"
	if err := store.Propose(routingapproval.Record{At: time.Now(), Key: key, Provider: "anthropic", From: "sonnet", To: "opus"}); err != nil {
		t.Fatal(err)
	}
	ask := func(mode presence.Mode, method, decision string) error {
		_, err := AnswerRouting(RoutingAnswer{StorePath: path, Key: key, Decision: decision, Approval: mode, Method: method})
		return err
	}
	if err := ask(presence.ModePresence, RoutingMethodMCP, "approve"); !IsInput(err) || !strings.Contains(err.Error(), "not through an agent") ||
		!strings.Contains(err.Error(), "tokenops routing decide 'anthropic|sonnet|opus' approve") {
		t.Fatalf("MCP approval under presence: %v", err)
	}
	if err := ask(presence.ModePresence, RoutingMethodCLI, "approve"); !IsInput(err) || !strings.Contains(err.Error(), "no presence key is enrolled") {
		t.Fatalf("unsigned terminal approval under presence: %v", err)
	}
	if err := ask(presence.ModePresence, RoutingMethodMCP, "deny"); err != nil {
		t.Fatalf("deny under presence: %v", err)
	}
	if err := ask(presence.ModeChannel, RoutingMethodMCP, "approve"); err != nil {
		t.Fatalf("approval under channel: %v", err)
	}
}
