package routeguard

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/governance/modelpolicy"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var offered = []string{"claude-haiku-4-5", "claude-sonnet-5", "claude-opus-5", "claude-fable-5-1"}

func policyInput(requested, session string, p modelpolicy.Policy) PolicyInput {
	return PolicyInput{
		Requested: requested, SessionModel: session,
		Provider: eventschema.ProviderAnthropic, Catalog: catalog(),
		Offered: offered, Policy: p,
	}
}

func TestEnforcePolicy(t *testing.T) {
	cases := []struct {
		name               string
		requested, session string
		policy             modelpolicy.Policy
		forbidden          bool
		wantAlias          string
	}{
		{"no policy", "opus", "", modelpolicy.Policy{}, false, ""},
		{"permitted request", "sonnet", "", modelpolicy.Policy{Deny: []string{"*opus*"}}, false, ""},
		// The alias is resolved against every offered model, so a deny
		// on the full id still catches it.
		{"alias of a denied id", "opus", "", modelpolicy.Policy{Deny: []string{"claude-opus-5"}}, true, "sonnet"},
		{"inherited session model", "", "claude-opus-5", modelpolicy.Policy{Deny: []string{"*opus*"}}, true, "sonnet"},
		{"closest below first", "sonnet", "", modelpolicy.Policy{Deny: []string{"*sonnet*"}}, true, "haiku"},
		{"above when nothing below", "haiku", "", modelpolicy.Policy{Deny: []string{"*haiku*"}}, true, "sonnet"},
		{"allow list", "opus", "", modelpolicy.Policy{Allow: []string{"*haiku*"}}, true, "haiku"},
		{"nothing permitted", "opus", "", modelpolicy.Policy{Allow: []string{"openai/*"}}, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := EnforcePolicy(policyInput(c.requested, c.session, c.policy))
			if d.Forbidden != c.forbidden || d.ToAlias != c.wantAlias {
				t.Fatalf("decision = %+v, want forbidden=%v alias=%q", d, c.forbidden, c.wantAlias)
			}
			if d.Forbidden && !strings.Contains(d.Reason, "model policy") {
				t.Errorf("reason does not name the policy: %q", d.Reason)
			}
		})
	}
}
