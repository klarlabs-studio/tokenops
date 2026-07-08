package pricing

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

const opusModel = "claude-opus-4-8"

// baselineOpusInput is the baseline Anthropic Opus 4.8 input rate, read from
// the embedded catalog so the test tracks the catalog instead of hardcoding.
func baselineOpusInput(t *testing.T) float64 {
	t.Helper()
	r, err := spend.DefaultTable().Lookup(eventschema.ProviderAnthropic, opusModel)
	if err != nil {
		t.Fatalf("baseline lookup: %v", err)
	}
	return r.InputPerMillion
}

func opusEvent() *eventschema.PromptEvent {
	return &eventschema.PromptEvent{
		Provider:     eventschema.ProviderAnthropic,
		RequestModel: opusModel,
		InputTokens:  1_000_000,
	}
}

func TestSnapshotTablePrefixMatchesVersionSuffix(t *testing.T) {
	s := Snapshot{FetchedAt: time.Now(), Rates: map[string]Rate{
		snapKey("anthropic", opusModel): {InputPerMillion: 12, OutputPerMillion: 60},
	}}
	tbl := s.Table()
	// A version-suffixed request model must resolve via the re-added "*".
	r, err := tbl.Lookup(eventschema.ProviderAnthropic, "claude-opus-4-8[1m]")
	if err != nil {
		t.Fatalf("suffixed lookup: %v", err)
	}
	if r.InputPerMillion != 12 {
		t.Errorf("prefix match lost rate: %+v", r)
	}
}

func TestSnapshotTableIsMultiProvider(t *testing.T) {
	// A snapshot spanning several providers must build a Key{Provider, Model}
	// row for each, and each must resolve under its own provider only.
	s := Snapshot{FetchedAt: time.Now(), Rates: map[string]Rate{
		snapKey("anthropic", opusModel):     {InputPerMillion: 5, OutputPerMillion: 25},
		snapKey("openai", "gpt-4o"):         {InputPerMillion: 2.5, OutputPerMillion: 10},
		snapKey("mistral", "mistral-large"): {InputPerMillion: 2, OutputPerMillion: 6},
	}}
	tbl := s.Table()
	cases := []struct {
		provider eventschema.Provider
		model    string
		want     float64
	}{
		{eventschema.ProviderAnthropic, "claude-opus-4-8", 5},
		{eventschema.ProviderOpenAI, "gpt-4o-2024-08-06", 2.5},
		{eventschema.ProviderMistral, "mistral-large-latest", 2},
	}
	for _, c := range cases {
		r, err := tbl.Lookup(c.provider, c.model)
		if err != nil {
			t.Errorf("lookup %s/%s: %v", c.provider, c.model, err)
			continue
		}
		if r.InputPerMillion != c.want {
			t.Errorf("%s/%s input = %v, want %v", c.provider, c.model, r.InputPerMillion, c.want)
		}
	}
	// A model must NOT resolve under the wrong provider.
	if _, err := tbl.Lookup(eventschema.ProviderOpenAI, "claude-opus-4-8"); err == nil {
		t.Error("opus resolved under openai; provider scoping lost")
	}
}

func TestSnapshotsToDatedTablesBaselineIsEarliestAndComplete(t *testing.T) {
	dated := SnapshotsToDatedTables(AllSnapshots(t.TempDir()))
	if len(dated) != 1 {
		t.Fatalf("baseline-only produced %d dated tables, want 1", len(dated))
	}
	if !dated[0].EffectiveFrom.Equal(baselineFetchedAt) {
		t.Errorf("baseline EffectiveFrom = %s, want %s", dated[0].EffectiveFrom, baselineFetchedAt)
	}
	// Completeness: a non-Anthropic provider must still price (the dated
	// table layers the Anthropic snapshot onto the full DefaultTable).
	if _, err := dated[0].Table.Lookup(eventschema.ProviderOpenAI, "gpt-4o-mini"); err != nil {
		t.Errorf("baseline dated table dropped non-Anthropic pricing: %v", err)
	}
}

func TestEffectiveEngineBaselineOnlyMatchesDefaultTable(t *testing.T) {
	eng, err := EffectiveEngine(t.TempDir())
	if err != nil {
		t.Fatalf("EffectiveEngine: %v", err)
	}
	flat := spend.NewEngine(spend.DefaultTable())

	// Price a spread of events across providers at various timestamps; the
	// baseline-only effective engine must agree with the flat engine exactly.
	events := []*eventschema.PromptEvent{
		{Provider: eventschema.ProviderAnthropic, RequestModel: opusModel, InputTokens: 500_000, OutputTokens: 100_000},
		{Provider: eventschema.ProviderOpenAI, RequestModel: "gpt-4o-mini", InputTokens: 1_000_000, OutputTokens: 1_000_000},
	}
	stamps := []time.Time{{}, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), time.Now()}
	for _, p := range events {
		want, werr := flat.Compute(p)
		for _, at := range stamps {
			got, gerr := eng.ComputeAt(p, at)
			if (werr == nil) != (gerr == nil) {
				t.Fatalf("err mismatch: flat=%v effective=%v", werr, gerr)
			}
			if want != got {
				t.Errorf("%s@%s: effective=%.6f want %.6f", p.RequestModel, at, got, want)
			}
		}
	}
}

func TestEffectiveEngineAppliesSnapshotAfterFetchDate(t *testing.T) {
	dir := t.TempDir()
	// A fetched snapshot dated well after the baseline, doubling Opus input.
	newInput := baselineOpusInput(t) * 2
	fetchedAt := baselineFetchedAt.Add(90 * 24 * time.Hour)
	if _, err := SaveSnapshot(dir, Snapshot{
		Source:    "litellm",
		FetchedAt: fetchedAt,
		Rates:     map[string]Rate{snapKey("anthropic", opusModel): {InputPerMillion: newInput, OutputPerMillion: 99}},
	}); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	eng, err := EffectiveEngine(dir)
	if err != nil {
		t.Fatalf("EffectiveEngine: %v", err)
	}

	// Two dated tables now: baseline + fetched, ordered by FetchedAt.
	dated := SnapshotsToDatedTables(AllSnapshots(dir))
	if len(dated) != 2 {
		t.Fatalf("want 2 dated tables, got %d", len(dated))
	}
	if dated[0].EffectiveFrom.After(dated[1].EffectiveFrom) {
		t.Error("dated tables not ordered ascending by EffectiveFrom")
	}

	p := opusEvent()
	before, _ := eng.ComputeAt(p, fetchedAt.Add(-time.Hour))
	after, _ := eng.ComputeAt(p, fetchedAt.Add(time.Hour))
	wantBefore := perM(baselineOpusInput(t))
	wantAfter := perM(newInput)
	if before != wantBefore {
		t.Errorf("before fetch: %.6f, want baseline %.6f", before, wantBefore)
	}
	if after != wantAfter {
		t.Errorf("after fetch: %.6f, want fetched %.6f", after, wantAfter)
	}
}

func TestEffectiveEngineSnapshotOverridesRightProvider(t *testing.T) {
	dir := t.TempDir()
	// A fetched snapshot changing ONLY a Mistral rate must override the Mistral
	// baseline row (not Anthropic) once effective, while Anthropic keeps pricing
	// on the baseline — proving the layering is by Key{Provider, Model}.
	fetchedAt := baselineFetchedAt.Add(90 * 24 * time.Hour)
	if _, err := SaveSnapshot(dir, Snapshot{
		Source:    "litellm",
		FetchedAt: fetchedAt,
		Rates:     map[string]Rate{snapKey("mistral", "mistral-large"): {InputPerMillion: 9, OutputPerMillion: 27}},
	}); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	eng, err := EffectiveEngine(dir)
	if err != nil {
		t.Fatalf("EffectiveEngine: %v", err)
	}

	mistral := &eventschema.PromptEvent{
		Provider: eventschema.ProviderMistral, RequestModel: "mistral-large-latest", InputTokens: 1_000_000,
	}
	before, _ := eng.ComputeAt(mistral, fetchedAt.Add(-time.Hour))
	after, _ := eng.ComputeAt(mistral, fetchedAt.Add(time.Hour))
	if before != perM(0.5) { // baseline mistral-large input = 0.5 (Large 3)
		t.Errorf("mistral before override: %.6f, want baseline %.6f", before, perM(0.5))
	}
	if after != perM(9) {
		t.Errorf("mistral after override: %.6f, want %.6f", after, perM(9))
	}

	// Anthropic must be untouched by a Mistral-only snapshot.
	opusAfter, _ := eng.ComputeAt(opusEvent(), fetchedAt.Add(time.Hour))
	if opusAfter != perM(baselineOpusInput(t)) {
		t.Errorf("anthropic drifted from a mistral-only snapshot: %.6f", opusAfter)
	}
}

func TestEffectiveEngineWithOverridesHonoredAcrossPeriods(t *testing.T) {
	dir := t.TempDir()
	overrides := spend.Table{Rates: map[spend.Key]spend.Rate{
		{Provider: eventschema.ProviderAnthropic, Model: opusModel + "*"}: {InputPerMillion: 3},
	}}
	eng, err := EffectiveEngineWithOverrides(dir, overrides)
	if err != nil {
		t.Fatalf("EffectiveEngineWithOverrides: %v", err)
	}
	p := opusEvent()
	got, _ := eng.ComputeAt(p, time.Now())
	if got != perM(3) {
		t.Errorf("override input rate not applied: got %.6f, want %.6f", got, perM(3))
	}
}

// perM converts a $/M input rate into the cost of a 1M-input-token event.
func perM(inputPerMillion float64) float64 {
	return float64(1_000_000) * inputPerMillion / 1_000_000.0
}
