package spend

import (
	_ "embed"
	"fmt"
	"maps"
	"os"
	"sync"

	"gopkg.in/yaml.v3"

	"go.klarlabs.de/tokenops/pkg/eventschema"
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
	// Verified marks a row as hand-checked against the vendor and therefore
	// authoritative: a fetched pricing snapshot must not override it (see
	// spend.DefaultPinnedKeys and pricing.SnapshotsToDatedTables). Purely
	// metadata — it does not affect the Rate's cost math.
	Verified bool `yaml:"verified"`
}

// ParseTable decodes a YAML pricing document (see pricing.yaml for the
// schema) into a Table. Unknown providers are kept verbatim — the
// Provider type is an open string, so override files may price vendors
// the event schema gains later.
func ParseTable(data []byte) (Table, error) {
	t, _, err := parseTableAndPins(data)
	return t, err
}

// parseTableAndPins decodes the pricing document into a Table and the set of
// Keys marked `verified: true`. The Verified flag is dropped from the Rate
// (which carries only cost fields) and surfaced separately.
func parseTableAndPins(data []byte) (Table, map[Key]bool, error) {
	var doc tableYAML
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Table{}, nil, fmt.Errorf("spend: parse pricing table: %w", err)
	}
	t := Table{
		Currency: doc.Currency,
		Rates:    make(map[Key]Rate),
	}
	pins := make(map[Key]bool)
	for provider, models := range doc.Rates {
		for model, r := range models {
			key := Key{eventschema.Provider(provider), model}
			t.Rates[key] = Rate{
				InputPerMillion:       r.InputPerMillion,
				OutputPerMillion:      r.OutputPerMillion,
				CachedInputPerMillion: r.CachedInputPerMillion,
			}
			if r.Verified {
				pins[key] = true
			}
		}
	}
	return t, pins, nil
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
	defaultPinsParsed  map[Key]bool
)

// loadDefault parses the embedded catalog once, caching both the table and the
// set of verified (pinned) keys.
func loadDefault() {
	t, pins, err := parseTableAndPins(defaultPricingYAML)
	if err != nil {
		// The embedded catalog ships with the binary; a parse failure is a
		// build defect, not a runtime condition. Guarded by TestDefaultTableParses.
		panic(err)
	}
	defaultTableParsed = t
	defaultPinsParsed = pins
}

// DefaultPinnedKeys returns the set of embedded-catalog rows marked
// `verified: true` — hand-checked against the vendor and therefore
// authoritative. A fetched pricing snapshot must not override these
// (pricing.SnapshotsToDatedTables strips them before layering the snapshot),
// so the source cannot silently regress a value we verified. Returns an
// independent copy.
func DefaultPinnedKeys() map[Key]bool {
	defaultTableOnce.Do(loadDefault)
	out := make(map[Key]bool, len(defaultPinsParsed))
	maps.Copy(out, defaultPinsParsed)
	return out
}

// DefaultTable returns a fresh Table seeded from the embedded
// pricing.yaml catalog. The values are intentionally conservative public
// list prices: callers running on negotiated rates should override via
// TableWithOverrides / MergeOverrides. Each call returns an independent
// map, so callers may merge without affecting process-wide state.
func DefaultTable() Table {
	defaultTableOnce.Do(loadDefault)
	out := Table{
		Currency: defaultTableParsed.Currency,
		Rates:    make(map[Key]Rate, len(defaultTableParsed.Rates)),
	}
	maps.Copy(out.Rates, defaultTableParsed.Rates)
	return out
}
