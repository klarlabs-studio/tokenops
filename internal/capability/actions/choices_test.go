package actions

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
)

func TestSetPreferredModel(t *testing.T) {
	path := sandbox(t, "log:\n  level: info\n")
	got, err := SetPreferredModel(path, "anthropic", "claude-sonnet-5", false)
	if err != nil || got.PreferredModels["anthropic"] != "claude-sonnet-5" {
		t.Fatalf("set = %+v, %v", got, err)
	}
	if got, err := SetPreferredModel(path, "anthropic", "", true); err != nil || len(got.PreferredModels) != 0 {
		t.Errorf("clear = %+v, %v", got, err)
	}
	for _, tc := range [][2]string{{"", "x"}, {"anthropic", ""}} {
		if _, err := SetPreferredModel(path, tc[0], tc[1], false); !IsInput(err) {
			t.Errorf("%v err = %v", tc, err)
		}
	}
}

func TestDecideRouting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approvals.jsonl")
	store, err := routingapproval.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Propose(routingapproval.Record{At: time.Now(), Key: "k", Provider: "anthropic", From: "sonnet", To: "opus"}); err != nil {
		t.Fatal(err)
	}
	if _, err := DecideRouting(path, "k", "maybe"); !IsInput(err) {
		t.Errorf("bad decision err = %v", err)
	}
	if _, err := DecideRouting(path, "nope", "approve"); !IsInput(err) {
		t.Errorf("unknown key err = %v", err)
	}
	got, err := DecideRouting(path, "k", "deny")
	if err != nil || got.Decision != "denied" || got.Model != "sonnet" {
		t.Fatalf("deny = %+v, %v", got, err)
	}
	if pending, _ := store.Pending(); len(pending) != 0 {
		t.Errorf("still pending: %+v", pending)
	}
}

func TestRecordOutcomeValidates(t *testing.T) {
	ctx := context.Background()
	if _, err := RecordOutcome(ctx, nil, OutcomeRequest{Result: "achieved"}); !IsInput(err) {
		t.Errorf("no execution err = %v", err)
	}
	if _, err := RecordOutcome(ctx, nil, OutcomeRequest{ExecutionID: "e", Result: "done"}); !IsInput(err) {
		t.Errorf("bad result err = %v", err)
	}
	neg := -1.0
	if _, err := RecordOutcome(ctx, nil, OutcomeRequest{ExecutionID: "e", Result: "partial", AttentionMinutes: &neg}); !IsInput(err) {
		t.Errorf("negative minutes err = %v", err)
	}
	got, err := RecordOutcome(ctx, nil, OutcomeRequest{ExecutionID: "e", Result: "achieved"})
	if err != nil || got.Error != "storage_disabled" || got.Recorded {
		t.Errorf("no store = %+v, %v", got, err)
	}
}
