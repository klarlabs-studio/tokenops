package routers

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// The gate refers an upgrade it has no answer for, applies the operator's
// answer once there is one, and records a fresh proposal for them.
func TestApprovalGateFollowsTheOperatorsAnswer(t *testing.T) {
	store, err := routingapproval.Open(filepath.Join(t.TempDir(), "approvals.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var rc Config
	AttachApprovalGate(&rc, config.Config{}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))

	p := router.Proposal{Provider: eventschema.ProviderAnthropic, FromModel: "claude-sonnet-5", ProposedModel: "claude-opus-5"}
	if got := rc.UpgradeDecision(p.Provider, p.FromModel, p.ProposedModel); got != router.DecisionPending {
		t.Errorf("unanswered upgrade: %q, want pending", got)
	}
	rc.OnProposal(p)
	pending, err := store.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending %v, err %v; want the proposal recorded", pending, err)
	}
	if err := store.Decide(p.Key(), string(router.DecisionApproved), p.ProposedModel); err != nil {
		t.Fatal(err)
	}
	if got := rc.UpgradeDecision(p.Provider, p.FromModel, p.ProposedModel); got != router.DecisionApproved {
		t.Errorf("approved upgrade: %q, want approved", got)
	}
}
