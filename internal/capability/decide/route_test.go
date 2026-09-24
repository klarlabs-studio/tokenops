package decide

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/contexts/policy"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestRouteShadowPreservesWhyWithoutPrompt(t *testing.T) {
	res := Route(RouteInput{
		ID: "decision:test", At: time.Unix(100, 0), Provider: eventschema.ProviderAnthropic,
		CurrentModel: "opus", Advice: router.Advice{Model: "sonnet", Class: "mechanical", Quality: .9, Reason: "capacity is scarce"},
		Authority: policy.ObserveOnly, Adapter: Adapter{Name: "mcp", CanApplyRoute: false},
		WindowKnown: true, WindowPct: 91,
	})
	if res.Stage != eventschema.DecisionStageShadow || res.Executable {
		t.Fatalf("shadow result = %+v", res)
	}
	b, err := json.Marshal(res.Event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret instruction") {
		t.Fatal("decision persisted prompt content")
	}
	p := res.Event.Payload.(*eventschema.DecisionEvent)
	if p.Rationale == "" || len(p.Alternatives) != 2 || len(p.Evidence) != 2 {
		t.Fatalf("decision lost explanation: %+v", p)
	}
}

func TestRoutePreservesLearnedBeliefAsEvidence(t *testing.T) {
	belief := &learning.Belief{Tier: learning.TierSupported, CompletedPairs: 5, Caveat: "recommendation only"}
	got := Route(RouteInput{
		Provider: eventschema.ProviderAnthropic, CurrentModel: "opus",
		Advice: router.Advice{Model: "sonnet", Quality: .9}, Authority: RecommendAuthority(),
		Adapter: Adapter{Name: "proxy", CanApplyRoute: true}, Belief: belief,
	})
	payload := got.Event.Payload.(*eventschema.DecisionEvent)
	if got.Stage != eventschema.DecisionStageProposed || payload.Executable {
		t.Fatalf("decision = %+v payload=%+v", got, payload)
	}
	found := false
	for _, evidence := range payload.Evidence {
		if evidence.Kind == "routing_belief" && evidence.Scope == string(learning.TierSupported) && evidence.Confidence == .8 {
			found = true
		}
	}
	if !found {
		t.Fatalf("learned evidence missing: %+v", payload.Evidence)
	}
}

func TestRouteCannotClaimExecutionWithoutAdapterCapability(t *testing.T) {
	base := RouteInput{
		ID: "decision:test", At: time.Unix(100, 0), Provider: eventschema.ProviderAnthropic,
		CurrentModel: "opus", Advice: router.Advice{Model: "sonnet", Class: "mechanical", Quality: .9, Reason: "eligible"},
		Authority: policy.Automatic,
	}
	native := Route(base)
	if native.Executable || native.Stage == eventschema.DecisionStageApplied {
		t.Fatal("native surface claimed it applied a route")
	}
	base.Adapter = Adapter{Name: "proxy", CanApplyRoute: true}
	proxied := Route(base)
	if !proxied.Executable || proxied.Stage != eventschema.DecisionStageApplied {
		t.Fatalf("proxy result = %+v", proxied)
	}
}
