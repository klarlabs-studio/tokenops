package config

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestModelPolicyFiltersRoutingCandidates(t *testing.T) {
	var cfg Config
	src := `
model_policy:
  deny: ["*opus*"]
optimizer:
  smart_routing:
    enabled: true
    models:
      anthropic: [claude-opus-5, claude-sonnet-5, claude-haiku-4-5]
`
	if err := yaml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatal(err)
	}
	got := cfg.RoutingCandidates(eventschema.ProviderAnthropic)
	if want := []string{"claude-sonnet-5", "claude-haiku-4-5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RoutingCandidates = %v, want %v", got, want)
	}
	rc := cfg.RouterConfig()
	if rc == nil || rc.Permits == nil {
		t.Fatal("router config carries no model policy")
	}
	if rc.Permits(eventschema.ProviderAnthropic, "claude-opus-5") {
		t.Error("router permits a denied model")
	}
	if !rc.Permits(eventschema.ProviderAnthropic, "claude-haiku-4-5") {
		t.Error("router refuses a permitted model")
	}
	if len(rc.Policy.Models[eventschema.ProviderAnthropic]) != 3 {
		t.Errorf("router does not see the models on offer: %v", rc.Policy.Models)
	}
}

func TestModelPolicyValidation(t *testing.T) {
	cfg := Default()
	cfg.ModelPolicy.Deny = []string{""}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "model_policy.deny[0]") {
		t.Fatalf("Validate = %v, want a model_policy error", err)
	}
}

// A policy alone still builds the router, so the proxy moves requests off
// forbidden models even with no routing configured.
func TestModelPolicyAloneBuildsTheRouter(t *testing.T) {
	cfg := Default()
	if cfg.RouterConfig() != nil {
		t.Fatal("router built with nothing to do")
	}
	cfg.ModelPolicy.Deny = []string{"*opus*"}
	rc := cfg.RouterConfig()
	if rc == nil || rc.Permits == nil || rc.Policy.Enabled {
		t.Fatalf("router config = %+v", rc)
	}
}
