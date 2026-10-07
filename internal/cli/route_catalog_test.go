package cli

import (
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// The route guard tiers models by the same card the cost engine prices
// with, negotiated rates included. It read the latest snapshot alone, so
// a model the operator pays little for still tiered as a flagship.
func TestRouteCatalogHonoursNegotiatedRates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	path := filepath.Join(t.TempDir(), "pricing.yaml")
	body := "currency: USD\nrates:\n  anthropic:\n    claude-opus-4-7:\n      input_per_million: 0.5\n      output_per_million: 0.5\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	cfg.Pricing.Path = path
	got := routeCatalog(cfg).Resolve(eventschema.ProviderAnthropic, "claude-opus-4-7")
	if got.CostPerMillion != 1 {
		t.Errorf("cost %.2f per million, want the negotiated 0.5 + 0.5", got.CostPerMillion)
	}
}
