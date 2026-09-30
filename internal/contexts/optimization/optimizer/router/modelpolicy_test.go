package router

import (
	"context"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func mechanicalAdvice(r *Router) Advice {
	return r.Advise(AdviceInput{
		Provider: eventschema.ProviderOpenAI, Model: "gpt-4o",
		Instruction: mechanicalInstruction, ToolDensity: 0.9,
	})
}

// The policy used to pick the cheapest model on the whole rate card,
// retired ones included. With the models on offer declared it picks only
// among them.
func TestPolicyPicksOnlyAmongOfferedModels(t *testing.T) {
	offered := []string{"gpt-4o", "gpt-4o-mini"}
	r := policyRouter(t, Policy{Enabled: true, Models: map[eventschema.Provider][]string{
		eventschema.ProviderOpenAI: offered,
	}}, 85, true)
	adv := mechanicalAdvice(r)
	if adv.Model != "gpt-4o-mini" {
		t.Fatalf("Model = %q (%s), want gpt-4o-mini, the cheapest on offer", adv.Model, adv.Reason)
	}
}

// A model the operator ruled out is never the target, however cheap.
func TestPolicySkipsForbiddenModels(t *testing.T) {
	unrestricted := mechanicalAdvice(policyRouter(t, Policy{Enabled: true}, 85, true)).Model
	if unrestricted == "" {
		t.Fatal("no baseline target")
	}
	r := New(Config{
		Policy:         Policy{Enabled: true},
		WindowPressure: func(eventschema.Provider) (float64, bool) { return 85, true },
		Permits:        func(_ eventschema.Provider, m string) bool { return m != unrestricted },
	}, spend.NewEngine(spend.DefaultTable()))
	adv := mechanicalAdvice(r)
	if adv.Model == unrestricted {
		t.Fatalf("routed to %q, which the policy forbids", adv.Model)
	}
}

// A rule whose target is forbidden falls through to a permitted
// fallback, and routes nowhere when there is none.
func TestRuleTargetMustBePermitted(t *testing.T) {
	rule := Rule{
		Provider: eventschema.ProviderOpenAI, FromModel: "gpt-4o",
		ToModel: "gpt-4o-mini", Fallbacks: []string{"gpt-4.1-mini"}, Quality: 0.9,
	}
	req := &optimizer.Request{Provider: eventschema.ProviderOpenAI, Model: "gpt-4o", Body: []byte(`{"model":"gpt-4o"}`)}
	for _, c := range []struct {
		name   string
		denied map[string]bool
		want   string
	}{
		{"falls back", map[string]bool{"gpt-4o-mini": true}, "gpt-4.1-mini"},
		{"nothing permitted", map[string]bool{"gpt-4o-mini": true, "gpt-4.1-mini": true}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := New(Config{
				Rules:   []Rule{rule},
				Permits: func(_ eventschema.Provider, m string) bool { return !c.denied[m] },
			}, nil)
			recs, err := r.Run(context.Background(), req)
			if err != nil || len(recs) != 1 {
				t.Fatalf("recs=%v err=%v", recs, err)
			}
			if recs[0].TargetModel != c.want {
				t.Fatalf("TargetModel = %q, want %q (%s)", recs[0].TargetModel, c.want, recs[0].Reason)
			}
			if c.want == "" && !strings.Contains(recs[0].Reason, "permitted") {
				t.Errorf("reason does not say why: %q", recs[0].Reason)
			}
		})
	}
}
