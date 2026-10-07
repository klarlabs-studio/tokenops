package config

import (
	"go.klarlabs.de/tokenops/internal/contexts/governance/modelpolicy"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// ModelPolicyConfig is the allow and deny lists, as glob patterns matched
// against "model" and "provider/model", ignoring case — "openai/*" rules
// out a vendor, "*opus*" a family.
type ModelPolicyConfig struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

// Policy is the domain form.
func (m ModelPolicyConfig) Policy() modelpolicy.Policy {
	return modelpolicy.Policy{Allow: m.Allow, Deny: m.Deny}
}

// RoutingCandidates is the models on offer for a provider
// (optimizer.smart_routing.models) that the model policy permits: the
// only set anything may route to.
func (c Config) RoutingCandidates(provider eventschema.Provider) []string {
	return c.ModelPolicy.Policy().Filter(provider, c.Optimizer.SmartRouting.Models[string(provider)])
}

// PreferredModel returns the operator's ceiling model for a provider, or
// "" when none is configured.
func (c Config) PreferredModel(provider eventschema.Provider) string {
	return c.PreferredModels[string(provider)]
}
