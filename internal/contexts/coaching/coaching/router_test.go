package coaching

import (
	"errors"
	"testing"

	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

func TestRouteBackendAnthropicPlanPicksHaiku(t *testing.T) {
	out, err := RouteBackend(RouterInputs{
		Plans:        map[string]string{"anthropic": "claude-max-20x"},
		PricingTable: spend.DefaultTable(),
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if out.Provider != eventschema.ProviderAnthropic {
		t.Errorf("provider=%s want anthropic", out.Provider)
	}
	// Cheapest anthropic in DefaultTable is claude-3-5-haiku. The
	// router must pick this without any hardcoded model name.
	if out.Model != "claude-3-5-haiku*" {
		t.Errorf("model=%q want claude-3-5-haiku* (cheapest)", out.Model)
	}
	if out.CredentialSource != "plan_session" {
		t.Errorf("creds=%q want plan_session", out.CredentialSource)
	}
}

func TestRouteBackendOpenAIPlanPicksMini(t *testing.T) {
	out, err := RouteBackend(RouterInputs{
		Plans:        map[string]string{"openai": "gpt-plus"},
		PricingTable: spend.DefaultTable(),
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if out.Model != "gpt-4o-mini*" {
		t.Errorf("model=%q want gpt-4o-mini* (cheapest openai)", out.Model)
	}
}

func TestRouteBackendPrefersAnthropicWhenMultipleConfigured(t *testing.T) {
	out, err := RouteBackend(RouterInputs{
		Plans: map[string]string{
			"openai":    "gpt-plus",
			"anthropic": "claude-max-20x",
			"gemini":    "claude-max-20x", // bogus pairing, still ok
		},
		PricingTable: spend.DefaultTable(),
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if out.Provider != eventschema.ProviderAnthropic {
		t.Errorf("multi-plan tiebreak picked %s, want anthropic", out.Provider)
	}
}

func TestRouteBackendFallsBackToLocal(t *testing.T) {
	probed := false
	out, err := RouteBackend(RouterInputs{
		Plans:        map[string]string{},
		PricingTable: spend.DefaultTable(),
		LocalProbe: func() (string, bool) {
			probed = true
			return "llama3.2:3b", true
		},
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if !probed {
		t.Error("LocalProbe was not called")
	}
	if out.Kind != "local" || out.Model != "llama3.2:3b" {
		t.Errorf("local fallback wrong: %+v", out)
	}
}

func TestRouteBackendNoBackendAvailable(t *testing.T) {
	_, err := RouteBackend(RouterInputs{
		Plans:        map[string]string{},
		PricingTable: spend.DefaultTable(),
		LocalProbe:   func() (string, bool) { return "", false },
	})
	if !errors.Is(err, ErrNoBackend) {
		t.Errorf("err=%v want ErrNoBackend", err)
	}
}

func TestRouteBackendUnknownProviderInPlansIsTolerated(t *testing.T) {
	// User has only Cursor configured — no rows for cursor in the
	// default pricing table, no local probe. Should ErrNoBackend
	// cleanly rather than panic.
	_, err := RouteBackend(RouterInputs{
		Plans:        map[string]string{"cursor": "cursor-pro"},
		PricingTable: spend.DefaultTable(),
		LocalProbe:   func() (string, bool) { return "", false },
	})
	if !errors.Is(err, ErrNoBackend) {
		t.Errorf("err=%v want ErrNoBackend", err)
	}
}
