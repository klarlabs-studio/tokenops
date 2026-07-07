package spend

import (
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// tableWithInput builds a single-model table priced at inputPerMillion for
// the given provider/model so a test can distinguish which dated table
// priced an event.
func tableWithInput(provider eventschema.Provider, model string, inputPerMillion float64) Table {
	return Table{
		Currency: "USD",
		Rates: map[Key]Rate{
			{provider, model}: {InputPerMillion: inputPerMillion},
		},
	}
}

// costOf prices a 1M-input-token event so the returned cost equals the
// selected table's InputPerMillion rate, making the effective rate legible.
func costOf(t *testing.T, e *Engine, at time.Time) float64 {
	t.Helper()
	p := &eventschema.PromptEvent{
		Provider:     eventschema.ProviderAnthropic,
		RequestModel: "claude-opus-4-8",
		InputTokens:  1_000_000,
	}
	cost, err := e.ComputeAt(p, at)
	if err != nil {
		t.Fatalf("ComputeAt(%s): %v", at, err)
	}
	return cost
}

func TestNewDatedEnginePicksGreatestEffectiveFromLEQTimestamp(t *testing.T) {
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	// Deliberately unsorted input to prove NewDatedEngine sorts internally.
	e := NewDatedEngine([]DatedTable{
		{EffectiveFrom: late, Table: tableWithInput(eventschema.ProviderAnthropic, "claude-opus-4-8", 30)},
		{EffectiveFrom: early, Table: tableWithInput(eventschema.ProviderAnthropic, "claude-opus-4-8", 10)},
		{EffectiveFrom: mid, Table: tableWithInput(eventschema.ProviderAnthropic, "claude-opus-4-8", 20)},
	})

	cases := []struct {
		name string
		at   time.Time
		want float64
	}{
		{"exactly at early boundary", early, 10},
		{"between early and mid", early.Add(24 * time.Hour), 10},
		{"exactly at mid boundary", mid, 20},
		{"between mid and late", mid.Add(24 * time.Hour), 20},
		{"exactly at late boundary", late, 30},
		{"after late", late.Add(365 * 24 * time.Hour), 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := costOf(t, e, tc.at); !approxEqual(got, tc.want) {
				t.Errorf("rate at %s = %.2f, want %.2f", tc.at, got, tc.want)
			}
		})
	}
}

func TestNewDatedEngineEventBeforeAllTablesUsesEarliest(t *testing.T) {
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	e := NewDatedEngine([]DatedTable{
		{EffectiveFrom: early, Table: tableWithInput(eventschema.ProviderAnthropic, "claude-opus-4-8", 10)},
		{EffectiveFrom: late, Table: tableWithInput(eventschema.ProviderAnthropic, "claude-opus-4-8", 30)},
	})
	before := early.Add(-100 * 24 * time.Hour)
	if got := costOf(t, e, before); !approxEqual(got, 10) {
		t.Errorf("event predating all tables priced at %.2f, want earliest 10", got)
	}
}

func TestNewDatedEngineZeroTimestampUsesEarliest(t *testing.T) {
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	e := NewDatedEngine([]DatedTable{
		{EffectiveFrom: late, Table: tableWithInput(eventschema.ProviderAnthropic, "claude-opus-4-8", 30)},
		{EffectiveFrom: early, Table: tableWithInput(eventschema.ProviderAnthropic, "claude-opus-4-8", 10)},
	})
	if got := costOf(t, e, time.Time{}); !approxEqual(got, 10) {
		t.Errorf("zero-timestamp event priced at %.2f, want earliest 10", got)
	}
	// Compute (no timestamp) must agree with the zero-timestamp path.
	p := &eventschema.PromptEvent{
		Provider: eventschema.ProviderAnthropic, RequestModel: "claude-opus-4-8", InputTokens: 1_000_000,
	}
	if got, err := e.Compute(p); err != nil || !approxEqual(got, 10) {
		t.Errorf("Compute = %.2f, err=%v, want earliest 10", got, err)
	}
}

func TestSingleTableEngineIsTimestampIndependent(t *testing.T) {
	// Backward-compat: NewEngine builds a one-table engine effective from the
	// zero time, so every event prices identically regardless of timestamp.
	e := NewEngine(tableWithInput(eventschema.ProviderAnthropic, "claude-opus-4-8", 42))
	stamps := []time.Time{
		{},
		time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		time.Now().Add(1000 * time.Hour),
	}
	for _, at := range stamps {
		if got := costOf(t, e, at); !approxEqual(got, 42) {
			t.Errorf("single-table engine at %s = %.2f, want 42", at, got)
		}
	}
}

func TestNewDatedEngineAuthoritativePerInstant(t *testing.T) {
	// A model present in the earlier table but absent from the later table
	// must NOT fall through to the earlier table once the later one is in
	// effect — the selected table is authoritative for that instant.
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	e := NewDatedEngine([]DatedTable{
		{EffectiveFrom: early, Table: tableWithInput(eventschema.ProviderAnthropic, "claude-retired", 10)},
		{EffectiveFrom: late, Table: tableWithInput(eventschema.ProviderAnthropic, "claude-opus-4-8", 30)},
	})
	p := &eventschema.PromptEvent{
		Provider: eventschema.ProviderAnthropic, RequestModel: "claude-retired", InputTokens: 1_000_000,
	}
	// Before late: the model exists in the earliest table → priced.
	if _, err := e.ComputeAt(p, early); err != nil {
		t.Errorf("early lookup should succeed: %v", err)
	}
	// After late: the effective table lacks the model → ErrUnknownModel,
	// not a fall-through to the earlier table.
	if _, err := e.ComputeAt(p, late); !errors.Is(err, ErrUnknownModel) {
		t.Errorf("late lookup = %v, want ErrUnknownModel (no fall-through)", err)
	}
}

func TestEffectiveDatingScenario(t *testing.T) {
	// Concrete scenario: baseline rate A effective early, one fetched
	// snapshot rate B effective at D. Events before D price at A; after, B.
	const rateA, rateB = 10.0, 25.0
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	e := NewDatedEngine([]DatedTable{
		{EffectiveFrom: early, Table: tableWithInput(eventschema.ProviderAnthropic, "claude-opus-4-8", rateA)},
		{EffectiveFrom: d, Table: tableWithInput(eventschema.ProviderAnthropic, "claude-opus-4-8", rateB)},
	})
	beforeD := d.Add(-time.Second)
	afterD := d.Add(time.Second)
	if got := costOf(t, e, beforeD); !approxEqual(got, rateA) {
		t.Errorf("event before D priced at %.2f, want A=%.2f", got, rateA)
	}
	if got := costOf(t, e, afterD); !approxEqual(got, rateB) {
		t.Errorf("event after D priced at %.2f, want B=%.2f", got, rateB)
	}
}

func TestNewDatedEngineEmptyDefaultsToEmptyTable(t *testing.T) {
	e := NewDatedEngine(nil)
	if e.Currency() != "USD" {
		t.Errorf("empty dated engine currency = %s, want USD", e.Currency())
	}
	p := &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: "x", InputTokens: 1}
	if _, err := e.Compute(p); !errors.Is(err, ErrUnknownModel) {
		t.Errorf("empty engine lookup = %v, want ErrUnknownModel", err)
	}
}

func TestEngineTableReturnsLatestEffective(t *testing.T) {
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	e := NewDatedEngine([]DatedTable{
		{EffectiveFrom: early, Table: tableWithInput(eventschema.ProviderAnthropic, "claude-opus-4-8", 10)},
		{EffectiveFrom: late, Table: tableWithInput(eventschema.ProviderAnthropic, "claude-opus-4-8", 30)},
	})
	r, err := e.Table().Lookup(eventschema.ProviderAnthropic, "claude-opus-4-8")
	if err != nil {
		t.Fatalf("latest table lookup: %v", err)
	}
	if r.InputPerMillion != 30 {
		t.Errorf("Table() returned rate %.2f, want latest-effective 30", r.InputPerMillion)
	}
}
