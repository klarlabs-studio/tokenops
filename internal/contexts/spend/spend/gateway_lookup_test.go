package spend

import (
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
