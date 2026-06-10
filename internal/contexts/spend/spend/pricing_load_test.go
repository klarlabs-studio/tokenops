package spend

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// Guards the panic path in DefaultTable: the embedded pricing.yaml must
// always parse and carry every provider the event schema knows.
func TestDefaultTableParses(t *testing.T) {
	tab := DefaultTable()
	if tab.Currency != "USD" {
		t.Errorf("currency = %q; want USD", tab.Currency)
	}
	if len(tab.Rates) == 0 {
		t.Fatal("embedded pricing.yaml produced an empty table")
	}
}

// Each call must return an independent map so callers can merge
// overrides without mutating process-wide state.
func TestDefaultTableReturnsIndependentCopies(t *testing.T) {
	a := DefaultTable()
	b := DefaultTable()
	k := Key{eventschema.ProviderAnthropic, "claude-fable-5*"}
	a.Rates[k] = Rate{InputPerMillion: 999}
	if b.Rates[k].InputPerMillion == 999 {
		t.Error("mutating one DefaultTable() leaked into another")
	}
}

// Newly released frontier models must resolve, including suffixed
// deployment variants via the prefix rows.
func TestDefaultTableCoversCurrentAnthropicModels(t *testing.T) {
	tab := DefaultTable()
	for model, want := range map[string]Rate{
		"claude-fable-5":     {InputPerMillion: 10.00, OutputPerMillion: 50.00, CachedInputPerMillion: 1.00},
		"claude-fable-5[1m]": {InputPerMillion: 10.00, OutputPerMillion: 50.00, CachedInputPerMillion: 1.00},
		"claude-opus-4-8":    {InputPerMillion: 5.00, OutputPerMillion: 25.00, CachedInputPerMillion: 0.50},
		"claude-opus-4-7":    {InputPerMillion: 5.00, OutputPerMillion: 25.00, CachedInputPerMillion: 0.50},
		"claude-opus-4-6":    {InputPerMillion: 5.00, OutputPerMillion: 25.00, CachedInputPerMillion: 0.50},
		"claude-sonnet-4-6":  {InputPerMillion: 3.00, OutputPerMillion: 15.00, CachedInputPerMillion: 0.30},
		"claude-haiku-4-5":   {InputPerMillion: 1.00, OutputPerMillion: 5.00, CachedInputPerMillion: 0.10},
	} {
		got, err := tab.Lookup(eventschema.ProviderAnthropic, model)
		if err != nil {
			t.Errorf("Lookup(%s): %v", model, err)
			continue
		}
		if got != want {
			t.Errorf("Lookup(%s) = %+v; want %+v", model, got, want)
		}
	}
}

func TestParseTableRejectsInvalidYAML(t *testing.T) {
	if _, err := ParseTable([]byte("rates: [not, a, map]")); err == nil {
		t.Error("expected error for malformed pricing YAML")
	}
}

func TestTableWithOverridesEmptyPathIsDefault(t *testing.T) {
	tab, err := TableWithOverrides("")
	if err != nil {
		t.Fatalf("TableWithOverrides: %v", err)
	}
	if len(tab.Rates) != len(DefaultTable().Rates) {
		t.Error("empty path should return the default table")
	}
}

func TestTableWithOverridesLayersFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pricing.yaml")
	doc := `
currency: USD
rates:
  anthropic:
    "claude-fable-5*":
      input_per_million: 8.00
  acme:
    "frontier-1*":
      input_per_million: 1.00
      output_per_million: 2.00
`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	tab, err := TableWithOverrides(path)
	if err != nil {
		t.Fatalf("TableWithOverrides: %v", err)
	}
	// Partial override: input changes, output/cached inherit from base.
	got, err := tab.Lookup(eventschema.ProviderAnthropic, "claude-fable-5")
	if err != nil {
		t.Fatalf("Lookup fable: %v", err)
	}
	want := Rate{InputPerMillion: 8.00, OutputPerMillion: 50.00, CachedInputPerMillion: 1.00}
	if got != want {
		t.Errorf("override rate = %+v; want %+v", got, want)
	}
	// New provider unknown to the default table still registers.
	if _, err := tab.Lookup(eventschema.Provider("acme"), "frontier-1-2026"); err != nil {
		t.Errorf("Lookup acme/frontier-1-2026: %v", err)
	}
}

func TestTableWithOverridesMissingFileErrors(t *testing.T) {
	if _, err := TableWithOverrides(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("expected error for missing override file")
	}
}
