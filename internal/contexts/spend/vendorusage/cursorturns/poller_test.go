package cursorturns

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/cursorturns"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func i64(v int64) *int64 { return &v }

func turn() cursorturns.Turn {
	return cursorturns.Turn{
		TS: time.Now().UTC(), ConversationID: "conv-1", GenerationID: "gen-1",
		Model: "cursor-grok-4.6-high-fast", ModelID: "grok-4.6",
		InputTokens: i64(1_000_000), OutputTokens: i64(5_000),
		CacheReadTokens: i64(900_000), CacheWriteTokens: i64(50_000),
	}
}

// Cursor's cache figures sit INSIDE input_tokens — its own team says so.
// The event schema keeps the two apart, so InputTokens must carry the
// whole input side exactly once and the cached figure ride alongside.
// Summing them would bill the cached tokens twice.
func TestEnvelopeDoesNotDoubleCountCache(t *testing.T) {
	env := NewEnvelope(turn(), eventschema.CostSourceMetered)
	if env == nil {
		t.Fatal("no envelope")
	}
	p, ok := env.Payload.(*eventschema.PromptEvent)
	if !ok {
		t.Fatalf("payload is %T, want *PromptEvent", env.Payload)
	}
	if p.InputTokens != 1_000_000 {
		t.Errorf("InputTokens = %d, want the reported input exactly once", p.InputTokens)
	}
	if p.CachedInputTokens != 900_000 {
		t.Errorf("CachedInputTokens = %d, want 900000", p.CachedInputTokens)
	}
	if p.TotalTokens != 1_005_000 {
		t.Errorf("TotalTokens = %d, want input+output with cache counted once", p.TotalTokens)
	}
}

// Cursor reports identical cumulative figures on stop and
// afterAgentResponse for one generation, and a follow-up refires the
// hook. The id is keyed on the generation so the store's ON CONFLICT
// drops the repeat — which is also why the poller can re-read the whole
// ledger without a marker.
func TestEnvelopeIDIsStablePerGeneration(t *testing.T) {
	a := NewEnvelope(turn(), eventschema.CostSourceMetered)
	b := turn()
	b.TS = b.TS.Add(time.Hour) // same generation, seen again later
	second := NewEnvelope(b, eventschema.CostSourceMetered)
	if a.ID != second.ID {
		t.Errorf("ids differ for one generation: %s vs %s", a.ID, second.ID)
	}
	c := turn()
	c.GenerationID = "gen-2"
	if NewEnvelope(c, eventschema.CostSourceMetered).ID == a.ID {
		t.Error("a different generation reused the same id")
	}
}

// Absent tokens mean "not reported", never a turn that cost nothing —
// so no event is made at all.
func TestEnvelopeSkipsUnreportedTurns(t *testing.T) {
	u := turn()
	u.InputTokens, u.OutputTokens = nil, nil
	if NewEnvelope(u, eventschema.CostSourceMetered) != nil {
		t.Error("an unmeasured turn became a spend event")
	}
}

// model_id is what a rate card might carry; the composer slug bakes in
// effort and speed flags none does. The slug is kept as an attribute
// rather than thrown away.
func TestEnvelopePrefersModelIDAndKeepsTheSlug(t *testing.T) {
	env := NewEnvelope(turn(), eventschema.CostSourceMetered)
	p, _ := env.Payload.(*eventschema.PromptEvent)
	if p.RequestModel != "grok-4.6" {
		t.Errorf("RequestModel = %q, want grok-4.6", p.RequestModel)
	}
	if env.Attributes["composer_slug"] != "cursor-grok-4.6-high-fast" {
		t.Errorf("composer slug lost: %q", env.Attributes["composer_slug"])
	}
}

// A flat-rate Cursor plan makes these plan-included, not metered — the
// distinction that keeps a subscription from reading as a bill.
func TestEnvelopeHonoursPlanCoverage(t *testing.T) {
	p, _ := NewEnvelope(turn(), eventschema.CostSourcePlanIncluded).Payload.(*eventschema.PromptEvent)
	if p.CostSource != eventschema.CostSourcePlanIncluded {
		t.Errorf("CostSource = %v, want plan-included", p.CostSource)
	}
}
