package spend

import (
	"errors"
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestLookupStripsGatewayNamespaceAndVariant(t *testing.T) {
	tbl := DefaultTable()
	for _, model := range []string{"kimi-k3", "accounts/fireworks/models/kimi-k3", "kimi-k3[1m]"} {
		r, err := tbl.Lookup(eventschema.ProviderFireworks, model)
		if err != nil || r.InputPerMillion != 3 || r.OutputPerMillion != 15 {
			t.Errorf("Lookup(fireworks, %q) = %+v, %v; want Kimi K3's $3/$15", model, r, err)
		}
	}
	// Exact keys: a Fast variant is never priced at the standard rate.
	if r, err := tbl.Lookup(eventschema.ProviderFireworks, "glm-5p3-fast"); err == nil {
		t.Errorf("glm-5p3-fast priced at %+v from a standard row", r)
	}
}

// A "-latest" alias is priced as the version the card keys; a router's
// ":free" variant costs nothing whether or not the card lists it.
func TestLookupResolvesLatestAliasesAndFreeVariants(t *testing.T) {
	tbl := Table{Rates: map[Key]Rate{
		{Provider: eventschema.ProviderOpenAI, Model: "gpt-5.2-chat"}:            {InputPerMillion: 1.75, OutputPerMillion: 14},
		{Provider: eventschema.ProviderOpenRouter, Model: "openai/gpt-oss-120b"}: {InputPerMillion: 0.1, OutputPerMillion: 0.5},
	}}
	if r, err := tbl.Lookup(eventschema.ProviderOpenAI, "gpt-5.2-chat-latest"); err != nil || r.InputPerMillion != 1.75 {
		t.Errorf("-latest alias = %+v, %v", r, err)
	}
	if r, err := tbl.Lookup(eventschema.ProviderOpenRouter, "openai/gpt-oss-120b:free"); err != nil || r != (Rate{}) {
		t.Errorf(":free variant = %+v, %v; want a zero rate", r, err)
	}
	if _, err := tbl.Lookup(eventschema.ProviderOpenAI, "gpt-unknown-latest"); !errors.Is(err, ErrUnknownModel) {
		t.Errorf("an alias of an unknown model stays unknown: %v", err)
	}
}
