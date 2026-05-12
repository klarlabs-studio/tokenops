package spend

import (
	"errors"
	"math"
	"testing"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

const epsilon = 1e-9

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) <= epsilon
}

func TestComputeOpenAIGPT4oMini(t *testing.T) {
	e := NewEngine(DefaultTable())
	p := &eventschema.PromptEvent{
		Provider:     eventschema.ProviderOpenAI,
		RequestModel: "gpt-4o-mini",
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
	}
	cost, err := e.Compute(p)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	want := 0.15 + 0.60
	if !approxEqual(cost, want) {
		t.Errorf("cost = %.6f, want %.6f", cost, want)
	}
}

func TestComputePrefersResponseModel(t *testing.T) {
	e := NewEngine(DefaultTable())
	p := &eventschema.PromptEvent{
		Provider:      eventschema.ProviderOpenAI,
		RequestModel:  "gpt-4o-mini", // cheap
		ResponseModel: "gpt-4o-2024-08-06",
		InputTokens:   1_000_000,
		OutputTokens:  0,
	}
	cost, err := e.Compute(p)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	// gpt-4o* input rate is $2.50/M, mini is $0.15/M — must bill the gpt-4o.
	if !approxEqual(cost, 2.50) {
		t.Errorf("cost = %.6f, want 2.50 (gpt-4o input rate)", cost)
	}
}

func TestComputeCachedInputBilling(t *testing.T) {
	e := NewEngine(DefaultTable())
	// 1M total input, 500K cached, 0 output. Cached is half rate ($0.075/M)
	// vs. regular ($0.15/M) for gpt-4o-mini.
	p := &eventschema.PromptEvent{
		Provider:          eventschema.ProviderOpenAI,
		RequestModel:      "gpt-4o-mini",
		InputTokens:       1_000_000,
		CachedInputTokens: 500_000,
	}
	cost, err := e.Compute(p)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	want := perMillion(500_000, 0.15) + perMillion(500_000, 0.075)
	if !approxEqual(cost, want) {
		t.Errorf("cost = %.6f, want %.6f", cost, want)
	}
}

func TestComputeCachedFallsBackToInputWhenZero(t *testing.T) {
	e := NewEngine(DefaultTable())
	// gpt-4-turbo has no cached rate set in default table.
	p := &eventschema.PromptEvent{
		Provider:          eventschema.ProviderOpenAI,
		RequestModel:      "gpt-4-turbo-2024-04-09",
		InputTokens:       1_000_000,
		CachedInputTokens: 1_000_000,
	}
	cost, err := e.Compute(p)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if !approxEqual(cost, 10.00) {
		t.Errorf("cost = %.6f, want 10.00 (full input rate)", cost)
	}
}

func TestComputeUnknownModel(t *testing.T) {
	e := NewEngine(DefaultTable())
	_, err := e.Compute(&eventschema.PromptEvent{
		Provider: eventschema.ProviderOpenAI, RequestModel: "nonsense-model",
	})
	if !errors.Is(err, ErrUnknownModel) {
		t.Errorf("err = %v, want ErrUnknownModel", err)
	}
}

func TestPrefixLookupChoosesLongestMatch(t *testing.T) {
	tab := Table{
		Currency: "USD",
		Rates: map[Key]Rate{
			{eventschema.ProviderOpenAI, "gpt-*"}:  {InputPerMillion: 1, OutputPerMillion: 1},
			{eventschema.ProviderOpenAI, "gpt-4*"}: {InputPerMillion: 5, OutputPerMillion: 5},
		},
	}
	r, err := tab.Lookup(eventschema.ProviderOpenAI, "gpt-4o-mini")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if r.InputPerMillion != 5 {
		t.Errorf("expected longer-prefix match (rate=5), got %f", r.InputPerMillion)
	}
}

func TestExactMatchBeatsPrefix(t *testing.T) {
	tab := Table{
		Currency: "USD",
		Rates: map[Key]Rate{
			{eventschema.ProviderOpenAI, "gpt-4o*"}:           {InputPerMillion: 2.50, OutputPerMillion: 10},
			{eventschema.ProviderOpenAI, "gpt-4o-2024-08-06"}: {InputPerMillion: 1.00, OutputPerMillion: 4},
		},
	}
	r, err := tab.Lookup(eventschema.ProviderOpenAI, "gpt-4o-2024-08-06")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if r.InputPerMillion != 1.00 {
		t.Errorf("exact match lost: %f", r.InputPerMillion)
	}
}

func TestApplyStampsCost(t *testing.T) {
	e := NewEngine(DefaultTable())
	p := &eventschema.PromptEvent{
		Provider: eventschema.ProviderAnthropic, RequestModel: "claude-sonnet-4-6",
		InputTokens: 100_000, OutputTokens: 50_000,
	}
	if err := e.Apply(p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := perMillion(100_000, 3.00) + perMillion(50_000, 15.00)
	if !approxEqual(p.CostUSD, want) {
		t.Errorf("CostUSD = %.6f, want %.6f", p.CostUSD, want)
	}
}

func TestApplyToEnvelopeIgnoresNonPrompt(t *testing.T) {
	e := NewEngine(DefaultTable())
	env := &eventschema.Envelope{
		Type: eventschema.EventTypeWorkflow,
		Payload: &eventschema.WorkflowEvent{
			WorkflowID: "wf", State: eventschema.WorkflowStateProgress,
		},
	}
	if err := e.ApplyToEnvelope(env); err != nil {
		t.Errorf("non-prompt envelope: err = %v", err)
	}
}

func TestApplyToEnvelopeNilSafe(t *testing.T) {
	e := NewEngine(DefaultTable())
	if err := e.ApplyToEnvelope(nil); err != nil {
		t.Errorf("nil envelope: %v", err)
	}
}

func TestMergeOverridesLayersRates(t *testing.T) {
	base := DefaultTable()
	override := Table{
		Rates: map[Key]Rate{
			{eventschema.ProviderOpenAI, "gpt-4o-mini*"}: {InputPerMillion: 0.05},
		},
	}
	merged := base.MergeOverrides(override)
	r, err := merged.Lookup(eventschema.ProviderOpenAI, "gpt-4o-mini")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if r.InputPerMillion != 0.05 {
		t.Errorf("override input lost: %f", r.InputPerMillion)
	}
	// Output rate should be inherited from base because override left it 0.
	if r.OutputPerMillion != 0.60 {
		t.Errorf("inherited output rate lost: %f", r.OutputPerMillion)
	}
}

func TestMergeOverridesAddsNewModel(t *testing.T) {
	base := DefaultTable()
	override := Table{
		Rates: map[Key]Rate{
			{eventschema.ProviderOpenAI, "custom-model-a"}: {
				InputPerMillion: 9, OutputPerMillion: 9,
			},
		},
	}
	merged := base.MergeOverrides(override)
	r, err := merged.Lookup(eventschema.ProviderOpenAI, "custom-model-a")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if r.InputPerMillion != 9 || r.OutputPerMillion != 9 {
		t.Errorf("new entry lost: %+v", r)
	}
}

func TestCurrencyOverride(t *testing.T) {
	tab := DefaultTable().MergeOverrides(Table{Currency: "EUR"})
	if NewEngine(tab).Currency() != "EUR" {
		t.Errorf("currency override lost: %s", tab.Currency)
	}
}

func TestEmptyEngineCurrencyDefault(t *testing.T) {
	e := NewEngine(Table{})
	if e.Currency() != "USD" {
		t.Errorf("default currency = %s, want USD", e.Currency())
	}
}

func TestComputeNilInput(t *testing.T) {
	e := NewEngine(DefaultTable())
	if _, err := e.Compute(nil); !errors.Is(err, ErrUnknownModel) {
		t.Errorf("nil prompt: err = %v", err)
	}
}

func TestPerMillionGuardsZero(t *testing.T) {
	if v := perMillion(0, 5); v != 0 {
		t.Errorf("zero tokens: %f", v)
	}
	if v := perMillion(1000, 0); v != 0 {
		t.Errorf("zero rate: %f", v)
	}
	if v := perMillion(-5, 5); v != 0 {
		t.Errorf("negative tokens: %f", v)
	}
}

func TestCachedExceedingInputClamps(t *testing.T) {
	e := NewEngine(DefaultTable())
	p := &eventschema.PromptEvent{
		Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o-mini",
		InputTokens: 1000, CachedInputTokens: 9999, // bogus reporter
	}
	cost, err := e.Compute(p)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	// All input clamped to cached; expect 1000 * 0.075 / 1M.
	want := perMillion(1000, 0.075)
	if !approxEqual(cost, want) {
		t.Errorf("cost = %.9f, want %.9f", cost, want)
	}
}

func TestDefaultTableHasExpectedProviders(t *testing.T) {
	tab := DefaultTable()
	for _, p := range []eventschema.Provider{
		eventschema.ProviderOpenAI, eventschema.ProviderAnthropic, eventschema.ProviderGemini,
	} {
		found := false
		for k := range tab.Rates {
			if k.Provider == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no rates for provider %s", p)
		}
	}
}
