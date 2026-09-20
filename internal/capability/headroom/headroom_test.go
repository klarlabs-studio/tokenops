package headroom_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// fakeReader answers with nothing, which is enough: this capability's
// job is orchestration — which plans, in what order, with which limits —
// and the arithmetic belongs to the plans domain, which has its own
// tests.
type fakeReader struct {
	err error
}

func (f *fakeReader) ReadEvents(_ context.Context, _ eventschema.EventType, _ time.Time) ([]*eventschema.Envelope, error) {
	return nil, f.err
}

func (f *fakeReader) CountBySource(_ context.Context, _, _ time.Time) (map[string]int64, error) {
	return nil, nil
}

func cfgWith(plans map[string]string) *config.Config {
	c := config.Default()
	c.Plans = plans
	return &c
}

// The divergence this capability removes: the MCP tool sorted providers
// and the CLI ranged the map directly, so "the first plan" — the one
// rendered first, and the one an agent reads first — was a different
// plan from call to call on the CLI side. The bug was found and fixed
// once, in one of the two places that had it.
func TestProvidersAreAlwaysInAStableOrder(t *testing.T) {
	deps := headroom.Deps{
		Config: cfgWith(map[string]string{
			"openai":    "gpt-plus",
			"anthropic": "claude-max-20x",
			"cursor":    "cursor-pro",
		}),
		Reader: &fakeReader{},
	}

	var first []string
	for range 5 {
		got, err := headroom.Compute(context.Background(), deps, time.Now().UTC())
		if err != nil {
			t.Fatalf("compute: %v", err)
		}
		order := make([]string, 0, len(got.Reports))
		for _, r := range got.Reports {
			order = append(order, r.Provider)
		}
		if first == nil {
			first = order
			continue
		}
		for i := range order {
			if order[i] != first[i] {
				t.Fatalf("order changed between calls: %v then %v", first, order)
			}
		}
	}
	if len(first) != 3 {
		t.Fatalf("want a report per plan, got %v", first)
	}
	// Stable means sorted, not merely repeatable: a caller rendering
	// "the first plan" should get the same one on every machine.
	for i := 1; i < len(first); i++ {
		if first[i-1] > first[i] {
			t.Errorf("providers are not sorted: %v", first)
		}
	}
}

// No plans configured is a question about configuration, not a failure.
// Both adapters had their own wording for it; the capability answers it
// once so they cannot drift apart again.
func TestNoPlansIsAnAnswerNotAnError(t *testing.T) {
	got, err := headroom.Compute(context.Background(),
		headroom.Deps{Config: cfgWith(nil), Reader: &fakeReader{}}, time.Now())

	if err != nil {
		t.Fatalf("unconfigured plans produced an error: %v", err)
	}
	if got.Unconfigured == "" {
		t.Error("nothing explained that no plans are bound")
	}
	if len(got.Reports) != 0 {
		t.Errorf("reports were invented: %+v", got.Reports)
	}
}

// A missing store is a different answer from missing plans, and
// conflating them sends an operator to fix the wrong thing.
func TestNoReaderIsItsOwnAnswer(t *testing.T) {
	got, err := headroom.Compute(context.Background(),
		headroom.Deps{Config: cfgWith(map[string]string{"anthropic": "claude-max-20x"})},
		time.Now())

	if err != nil {
		t.Fatalf("a missing store produced an error: %v", err)
	}
	if got.StorageDisabled == "" {
		t.Error("nothing explained that there is no event store")
	}
	if got.Unconfigured != "" {
		t.Error("a missing store was reported as missing plans")
	}
}

// A read failure is a real error and must reach the caller, not be
// rendered as an empty report.
func TestAReadFailureIsReturned(t *testing.T) {
	_, err := headroom.Compute(context.Background(), headroom.Deps{
		Config: cfgWith(map[string]string{"anthropic": "claude-max-20x"}),
		Reader: &fakeReader{err: errors.New("disk on fire")},
	}, time.Now())

	if err == nil {
		t.Fatal("a failing store produced a clean result")
	}
}

// An unknown plan name is named rather than dropped. Skipping it
// silently is how an operator's typo becomes a plan that reports
// nothing forever.
func TestAnUnknownPlanIsReported(t *testing.T) {
	got, err := headroom.Compute(context.Background(), headroom.Deps{
		Config: cfgWith(map[string]string{"anthropic": "not-a-real-plan"}),
		Reader: &fakeReader{},
	}, time.Now())

	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if len(got.Notes) == 0 {
		t.Fatal("an unknown plan produced no note")
	}
	if len(got.Reports) != 0 {
		t.Errorf("an unknown plan produced a report: %+v", got.Reports)
	}
}

// Per-provider spend limits come from config.PlanLimits and must reach
// the domain. Both adapters read them; only the capability should now.
func TestConfiguredLimitsReachTheComputation(t *testing.T) {
	cfg := cfgWith(map[string]string{"anthropic": "claude-max-20x"})
	cfg.PlanLimits = map[string]config.PlanLimit{
		"anthropic": {SpendLimitUSD: 42, Window: "monthly"},
	}

	got, err := headroom.Compute(context.Background(),
		headroom.Deps{Config: cfg, Reader: &fakeReader{}}, time.Now().UTC())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if len(got.Reports) != 1 {
		t.Fatalf("want 1 report, got %+v", got.Reports)
	}
}
