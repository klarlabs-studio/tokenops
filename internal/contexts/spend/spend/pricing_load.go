package spend

import (
	_ "embed"
	"fmt"
	"maps"
	"os"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// defaultPricingYAML is the embedded list-price catalog. Model rates
// live in pricing.yaml so a model launch is a data edit, not a code
// change.
//
//go:embed pricing.yaml
var defaultPricingYAML []byte

// tableYAML mirrors the on-disk pricing schema:
//
//	currency: USD
//	rates:
//	  anthropic:
//	    "claude-fable-5*":
//	      input_per_million: 10.00
//	      output_per_million: 50.00
//	      cached_input_per_million: 1.00
type tableYAML struct {
	Currency string                         `yaml:"currency"`
	Rates    map[string]map[string]rateYAML `yaml:"rates"`
}

type rateYAML struct {
	InputPerMillion       float64 `yaml:"input_per_million"`
	OutputPerMillion      float64 `yaml:"output_per_million"`
	CachedInputPerMillion float64 `yaml:"cached_input_per_million"`
}

// ParseTable decodes a YAML pricing document (see pricing.yaml for the
// schema) into a Table. Unknown providers are kept verbatim — the
// Provider type is an open string, so override files may price vendors
// the event schema gains later.
func ParseTable(data []byte) (Table, error) {
	var doc tableYAML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Table{}, fmt.Errorf("spend: parse pricing table: %w", err)
	}
	t := Table{
		Currency: doc.Currency,
		Rates:    make(map[Key]Rate),
	}
	for provider, models := range doc.Rates {
		for model, r := range models {
			t.Rates[Key{eventschema.Provider(provider), model}] = Rate(r)
		}
	}
	return t, nil
}

// LoadTableFile reads and parses a pricing YAML file from disk.
func LoadTableFile(path string) (Table, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Table{}, fmt.Errorf("spend: read pricing table %q: %w", path, err)
	}
	t, err := ParseTable(data)
	if err != nil {
		return Table{}, fmt.Errorf("spend: pricing table %q: %w", path, err)
	}
	return t, nil
}

// TableWithOverrides returns DefaultTable layered with the override file
// at path (same YAML schema; partial Rate fields inherit from the base
// row per Rate.Effective). Empty path returns DefaultTable unchanged.
func TableWithOverrides(path string) (Table, error) {
	base := DefaultTable()
	if path == "" {
		return base, nil
	}
	overrides, err := LoadTableFile(path)
	if err != nil {
		return Table{}, err
	}
	return base.MergeOverrides(overrides), nil
}

var (
	defaultTableOnce   sync.Once
	defaultTableParsed Table
)

// DefaultTable returns a fresh Table seeded from the embedded
// pricing.yaml catalog. The values are intentionally conservative public
// list prices: callers running on negotiated rates should override via
// TableWithOverrides / MergeOverrides. Each call returns an independent
// map, so callers may merge without affecting process-wide state.
func DefaultTable() Table {
	defaultTableOnce.Do(func() {
		t, err := ParseTable(defaultPricingYAML)
		if err != nil {
			// The embedded catalog ships with the binary; a parse failure
			// is a build defect, not a runtime condition. Guarded by
			// TestDefaultTableParses.
			panic(err)
		}
		defaultTableParsed = t
	})
	out := Table{
		Currency: defaultTableParsed.Currency,
		Rates:    make(map[Key]Rate, len(defaultTableParsed.Rates)),
	}
	maps.Copy(out.Rates, defaultTableParsed.Rates)
	return out
}
