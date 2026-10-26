package analytics_test

import (
	"context"
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A session turn's API-equivalent value prices its cache writes at the
// write rate, whether the event carries them in the payload or, written
// before that field existed, as Claude Code's cache_creation_input
// attribute.
func TestSessionTurnsPriceCacheWrites(t *testing.T) {
	turn := func(id string, payloadWrites int64, attrs map[string]string) *eventschema.Envelope {
		return &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: time.Now().UTC().Add(-time.Hour), Attributes: attrs,
			Payload: &eventschema.PromptEvent{
				Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-5", SessionID: "s-" + id,
				InputTokens: 1_000_000, TotalTokens: 1_000_000, CacheWriteInputTokens: payloadWrites,
				CostSource: eventschema.CostSourcePlanIncluded,
			},
		}
	}
	st := storeWith(t,
		turn("payload", 1_000_000, nil),
		turn("legacy", 0, map[string]string{"cache_creation_input": "1000000"}),
	)
	turns, err := analytics.New(st, spend.NewEngine(spend.DefaultTable())).SessionTurns(context.Background(),
		analytics.Filter{Since: time.Now().Add(-24 * time.Hour)})
	if err != nil {
		t.Fatalf("session turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("got %d turns, want 2", len(turns))
	}
	for _, tn := range turns {
		// claude-sonnet-5 writes are $2.50 per million against $2 input.
		if !tn.Priced || math.Abs(tn.APIEquivalentUSD-2.5) > 1e-9 {
			t.Errorf("%s: value = %v (priced %v), want 2.5", tn.SessionID, tn.APIEquivalentUSD, tn.Priced)
		}
	}
}

// A turn carries the workflow and execution it was recorded under, so
// work can be joined to the outcome recorded for it.
func TestSessionTurnsCarryWorkflowAndExecution(t *testing.T) {
	env := &eventschema.Envelope{
		ID: "linked", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp:   time.Now().UTC().Add(-time.Hour),
		Association: eventschema.Association{Execution: "exec-1"},
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-5", SessionID: "s-1",
			WorkflowID: "wf-1", InputTokens: 10, TotalTokens: 10,
		},
	}
	turns, err := analytics.New(storeWith(t, env), nil).SessionTurns(context.Background(),
		analytics.Filter{Since: time.Now().Add(-24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].WorkflowID != "wf-1" || turns[0].ExecutionID != "exec-1" {
		t.Fatalf("turns = %+v", turns)
	}
}

// The summary's API-equivalent value prices plan-covered cache writes at
// the write rate.
func TestSummarizePricesCacheWrites(t *testing.T) {
	env := planEvent("w", "claude-sonnet-5", 1_000_000, 0)
	env.Payload.(*eventschema.PromptEvent).CacheWriteInputTokens = 1_000_000
	got, err := analytics.New(storeWith(t, env), spend.NewEngine(spend.DefaultTable())).
		Summarize(context.Background(), analytics.Filter{Since: time.Now().Add(-24 * time.Hour)})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if math.Abs(got.APIEquivalentUSD-2.5) > 1e-9 {
		t.Errorf("APIEquivalentUSD = %v, want 2.5", got.APIEquivalentUSD)
	}
}

// A turn carries the client that recorded it, the route it went through
// and whether a plan covered it, so a rise in spend can be decomposed by
// tool and biller. Metered usage stored without a cost is billed at its
// list price, as DailyCharges bills it.
func TestSessionTurnsCarryToolRouteAndBilling(t *testing.T) {
	ev := func(id string, covered bool) *eventschema.Envelope {
		p := &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-5", SessionID: "s-" + id,
			InputTokens: 1_000_000, TotalTokens: 1_000_000,
		}
		if covered {
			p.CostSource = eventschema.CostSourcePlanIncluded
		}
		return &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: time.Now().UTC().Add(-time.Hour), Source: "claude-code-jsonl",
			Attributes: map[string]string{"endpoint": "anthropic-api"}, Payload: p,
		}
	}
	turns, err := analytics.New(storeWith(t, ev("plan", true), ev("metered", false)), spend.NewEngine(spend.DefaultTable())).
		SessionTurns(context.Background(), analytics.Filter{Since: time.Now().Add(-24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %+v", turns)
	}
	for _, tn := range turns {
		if tn.Source != "claude-code-jsonl" || tn.Endpoint != "anthropic-api" {
			t.Errorf("%s: source %q endpoint %q", tn.SessionID, tn.Source, tn.Endpoint)
		}
		switch tn.SessionID {
		case "s-plan":
			if !tn.PlanCovered || tn.BilledUSD() != 0 {
				t.Errorf("plan turn: covered %v billed %v", tn.PlanCovered, tn.BilledUSD())
			}
		case "s-metered":
			if tn.PlanCovered || math.Abs(tn.BilledUSD()-2) > 1e-9 {
				t.Errorf("metered turn: covered %v billed %v, want 2", tn.PlanCovered, tn.BilledUSD())
			}
		}
	}
}

// A turn carries its token breakdown, so a cache rebuild can be told from
// an ordinary turn and a turn repriced on another model.
func TestSessionTurnsCarryTheirTokenBreakdown(t *testing.T) {
	env := &eventschema.Envelope{
		ID: "split", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: time.Now().UTC().Add(-time.Hour),
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-5", SessionID: "s-1",
			InputTokens: 1_000, CachedInputTokens: 600, CacheWriteInputTokens: 300, CacheWrite1hInputTokens: 100,
			OutputTokens: 50, TotalTokens: 1_050,
		},
	}
	turns, err := analytics.New(storeWith(t, env), nil).SessionTurns(context.Background(),
		analytics.Filter{Since: time.Now().Add(-24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %+v", turns)
	}
	got := turns[0]
	if got.InputTokens != 1_000 || got.OutputTokens != 50 || got.CacheReadTokens != 600 ||
		got.CacheWriteTokens != 300 || got.CacheWrite1hTokens != 100 {
		t.Errorf("breakdown = %+v", got)
	}
}

// A turn carries how long it took, so the cache's idle gap can run to
// when its request went out rather than when it was recorded.
func TestSessionTurnsCarryLatency(t *testing.T) {
	env := &eventschema.Envelope{
		ID: "timed", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: time.Now().UTC().Add(-time.Hour),
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-5", SessionID: "s-1",
			InputTokens: 10, TotalTokens: 10, Latency: 3 * time.Minute,
		},
	}
	turns, err := analytics.New(storeWith(t, env), nil).SessionTurns(context.Background(),
		analytics.Filter{Since: time.Now().Add(-24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].Latency != 3*time.Minute {
		t.Fatalf("turns = %+v", turns)
	}
}
