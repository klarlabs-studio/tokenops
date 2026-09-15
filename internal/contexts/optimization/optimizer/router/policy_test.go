package router

import (
	"context"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// mechanicalTurn is a terse instruction over mostly tool traffic — the
// one shape the classifier calls mechanical without hesitation.
const mechanicalInstruction = "fix it"

func policyRouter(t *testing.T, pol Policy, windowPct float64, windowKnown bool) *Router {
	t.Helper()
	return New(Config{
		Policy: pol,
		WindowPressure: func(eventschema.Provider) (float64, bool) {
			return windowPct, windowKnown
		},
	}, spend.NewEngine(spend.DefaultTable()))
}

// The point of the policy: nobody wrote a from/to pair, and a route still
// happens when the turn and the window both justify it.
func TestPolicyRoutesWithoutAnyRule(t *testing.T) {
	r := policyRouter(t, Policy{Enabled: true}, 85, true)
	adv := r.Advise(AdviceInput{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Instruction: mechanicalInstruction, ToolDensity: 0.9,
	})
	if adv.Stay {
		t.Fatalf("advised staying on a mechanical turn at 85%% window: %q", adv.Reason)
	}
	if adv.Model == "" || adv.Model == "gpt-4o" {
		t.Fatalf("Model = %q, want a cheaper concrete model", adv.Model)
	}
	if strings.Contains(adv.Model, "*") {
		t.Errorf("Model %q is a pricing wildcard, not a name any API accepts", adv.Model)
	}
	if !strings.Contains(adv.Reason, "85%") {
		t.Errorf("reason does not name the measured window: %q", adv.Reason)
	}
}

// Reasoning work is left alone whatever the window says. Routing a
// reasoning turn down is the quality trade the operator did not ask for,
// and it is the one the whole classifier exists to avoid making.
func TestPolicyLeavesReasoningWorkAlone(t *testing.T) {
	r := policyRouter(t, Policy{Enabled: true}, 99, true)
	adv := r.Advise(AdviceInput{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Instruction: strings.Repeat("specify the exact behaviour here ", 30), ToolDensity: 0.1,
	})
	if !adv.Stay {
		t.Fatalf("routed a reasoning turn down to %q at a full window", adv.Model)
	}
	if adv.Class != "reasoning" {
		t.Errorf("Class = %q, want reasoning", adv.Class)
	}
}

// An unmeasured window is not a full one. This meter read 0/200 for
// months on a real machine; conserving on the strength of it would have
// degraded quality to relieve a shortage that was not happening.
func TestPolicyStaysWhenTheWindowIsUnmeasured(t *testing.T) {
	r := policyRouter(t, Policy{Enabled: true}, 0, false)
	adv := r.Advise(AdviceInput{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Instruction: mechanicalInstruction, ToolDensity: 0.9,
	})
	if !adv.Stay {
		t.Fatalf("routed down with no window reading: %q", adv.Reason)
	}
	if adv.WindowKnown {
		t.Error("WindowKnown = true with no reading")
	}
	if !strings.Contains(adv.Reason, "not being measured") {
		t.Errorf("reason does not say the window is unmeasured: %q", adv.Reason)
	}
}

// On a flat-rate plan a request costs nothing at the margin, so routing
// down while there is headroom trades quality for a saving that does not
// exist. There is deliberately no "conserve always".
func TestPolicyStaysBelowTheWindowThreshold(t *testing.T) {
	r := policyRouter(t, Policy{Enabled: true}, 40, true)
	adv := r.Advise(AdviceInput{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Instruction: mechanicalInstruction, ToolDensity: 0.9,
	})
	if !adv.Stay {
		t.Fatalf("conserved at 40%% window: %q", adv.Reason)
	}
	if !strings.Contains(adv.Reason, "40%") {
		t.Errorf("reason does not name the reading it declined on: %q", adv.Reason)
	}
}

// Off is the behaviour that shipped before the policy existed, and an
// abstention says so rather than going quiet.
func TestPolicyDisabledSaysSo(t *testing.T) {
	r := policyRouter(t, Policy{}, 99, true)
	adv := r.Advise(AdviceInput{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Instruction: mechanicalInstruction, ToolDensity: 0.9,
	})
	if !adv.Stay {
		t.Fatal("a disabled policy routed something")
	}
	if !strings.Contains(adv.Reason, "off") {
		t.Errorf("reason does not say the policy is off: %q", adv.Reason)
	}
}

// The preferred model is a ceiling in both directions: the policy cannot
// propose something pricier, and it does not need a blessing to go
// cheaper.
func TestPolicyNeverProposesPastThePreferredCeiling(t *testing.T) {
	cheap, ok := policyRouter(t, Policy{Enabled: true}, 90, true).cheapestTarget(eventschema.ProviderOpenAI)
	if !ok {
		t.Skip("no priced openai models in the catalog")
	}
	r := New(Config{
		Policy:         Policy{Enabled: true},
		WindowPressure: func(eventschema.Provider) (float64, bool) { return 90, true },
		// The operator's ceiling IS the cheapest model, so there is
		// nowhere cheaper for the policy to go.
		PreferredModel: func(eventschema.Provider) string { return cheap },
	}, spend.NewEngine(spend.DefaultTable()))

	adv := r.Advise(AdviceInput{
		Provider: eventschema.ProviderOpenAI, Model: cheap,
		Instruction: mechanicalInstruction, ToolDensity: 0.9,
	})
	if !adv.Stay {
		t.Fatalf("routed away from the cheapest model: %q", adv.Model)
	}
}

// Advice and enforcement run the same policy. Two verdicts about one turn
// would surprise the operator twice.
func TestPolicyAppliesInTheRequestPathToo(t *testing.T) {
	r := policyRouter(t, Policy{Enabled: true}, 85, true)
	body := bodyWithModel(t, "gpt-4o", map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": mechanicalInstruction},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "name": "Edit"},
			}},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "content": "ok"},
			}},
		},
	})
	recs, err := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o", Body: body,
		InputTokens: 1_000_000, OutputTokens: 100_000,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recs = %d, want 1 — the policy should have routed with no rule written", len(recs))
	}
	if !strings.Contains(recs[0].Reason, "gpt-4o ->") {
		t.Errorf("reason: %q", recs[0].Reason)
	}
	adv := r.Advise(AdviceInput{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Instruction: mechanicalInstruction, ToolDensity: 0.9,
	})
	if adv.Stay {
		t.Error("the request path routed and the advisory path said stay; they must agree")
	}
}

// An unmatched request under a disabled policy is left alone, exactly as
// before the policy existed.
func TestUnmatchedRequestUntouchedWithoutPolicy(t *testing.T) {
	r := New(Config{}, spend.NewEngine(spend.DefaultTable()))
	recs, err := r.Run(context.Background(), &optimizer.Request{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Body: bodyWithModel(t, "gpt-4o", nil),
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("recs = %d, want 0 — nothing was configured", len(recs))
	}
}

// The rate card prices most families by prefix. A literal asterisk is
// not a name any API accepts, so a target never carries one.
func TestCheapestTargetNeverCarriesAWildcard(t *testing.T) {
	r := New(Config{}, spend.NewEngine(spend.DefaultTable()))
	for _, p := range []eventschema.Provider{eventschema.ProviderOpenAI, eventschema.ProviderAnthropic} {
		if m, ok := r.cheapestTarget(p); ok && strings.Contains(m, "*") {
			t.Errorf("cheapestTarget(%s) = %q, a pricing wildcard", p, m)
		}
	}
}
