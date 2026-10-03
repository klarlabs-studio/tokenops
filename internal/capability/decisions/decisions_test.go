package decisions

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

func TestPendingProposalsAsksTheQuestion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := routingapproval.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Propose(routingapproval.Record{At: time.Now(), Key: "anthropic|sonnet|opus", Provider: "anthropic",
		From: "sonnet", To: "opus", Preferred: "sonnet", Priced: true, DeltaUSD: 12, Reason: "hard task"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Propose(routingapproval.Record{At: time.Now(), Key: "openai|a|b", Provider: "openai", From: "a", To: "b", Preferred: "a"}); err != nil {
		t.Fatal(err)
	}
	got, err := PendingProposals(path)
	if err != nil || len(got.Pending) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, p := range got.Pending {
		switch p.Key {
		case "anthropic|sonnet|opus":
			if p.ExtraUSDPerMillion == nil || *p.ExtraUSDPerMillion != 12 || p.Question == "" {
				t.Errorf("priced proposal %+v", p)
			}
		case "openai|a|b":
			if p.ExtraUSDPerMillion != nil || p.Pricing == "" {
				t.Errorf("unpriced proposal %+v", p)
			}
		}
	}
	empty, err := PendingProposals(filepath.Join(t.TempDir(), "none.jsonl"))
	if err != nil || len(empty.Pending) != 0 || empty.Note == "" {
		t.Errorf("empty = %+v, %v", empty, err)
	}
}

func TestExplainAnswersEveryCase(t *testing.T) {
	ctx := context.Background()
	if _, err := Explain(ctx, nil, "  "); !errors.Is(err, ErrMissingID) {
		t.Errorf("blank id err = %v", err)
	}
	if res, _ := Explain(ctx, nil, "d1"); res.Error != "storage_disabled" {
		t.Errorf("no store = %+v", res)
	}
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "e.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if res, _ := Explain(ctx, store, "decision:unknown"); res.Error != "decision_not_found" {
		t.Errorf("unknown = %+v", res)
	}
}
