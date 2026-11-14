package spend

import (
	"errors"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// memoCards are three dated cards: a model priced on every card, one by a
// wildcard on the middle card only, and one by a gateway name.
func memoCards(scale float64) []DatedTable {
	jan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	card := func(rates map[Key]Rate) Table { return Table{Currency: "USD", Rates: rates} }
	return []DatedTable{
		{EffectiveFrom: jan, Table: card(map[Key]Rate{
			{eventschema.ProviderOpenAI, "gpt-5"}: {InputPerMillion: 1 * scale, OutputPerMillion: 2 * scale},
		})},
		{EffectiveFrom: jan.AddDate(0, 2, 0), Table: card(map[Key]Rate{
			{eventschema.ProviderOpenAI, "gpt-5"}:            {InputPerMillion: 3 * scale, OutputPerMillion: 4 * scale},
			{eventschema.ProviderAnthropic, "claude-opus-*"}: {InputPerMillion: 5 * scale, OutputPerMillion: 25 * scale},
			{eventschema.ProviderFireworks, "kimi-k3"}:       {InputPerMillion: 0.5 * scale, OutputPerMillion: 2 * scale},
		})},
		{EffectiveFrom: jan.AddDate(0, 4, 0), Table: card(map[Key]Rate{
			{eventschema.ProviderOpenAI, "gpt-5"}: {InputPerMillion: 6 * scale, OutputPerMillion: 7 * scale},
		})},
	}
}

// LookupAt answers, memo or not, what looking the rate up on the cards
// answers, at every instant and for every model, and asked again; and
// after Replace, what the new cards answer.
func TestLookupAtMemoMatchesTheCards(t *testing.T) {
	models := []struct {
		provider eventschema.Provider
		model    string
	}{
		{eventschema.ProviderOpenAI, "gpt-5"},
		{eventschema.ProviderAnthropic, "claude-opus-5"},
		{eventschema.ProviderFireworks, "accounts/fireworks/models/kimi-k3"},
		{eventschema.ProviderOpenAI, "unknown-model"},
		{eventschema.ProviderOpenAI, ""},
		{eventschema.ProviderOpenRouter, "openai/gpt-oss:free"},
	}
	jan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	instants := []time.Time{{}, jan.AddDate(-1, 0, 0), jan, jan.AddDate(0, 1, 0), jan.AddDate(0, 2, 0), jan.AddDate(0, 3, 0), jan.AddDate(0, 6, 0)}
	eng := NewDatedEngine(memoCards(1))
	check := func(step string, cards []DatedTable) {
		t.Helper()
		want := normalizeTables(cards)
		for range 2 { // the second pass answers from the memo
			for _, m := range models {
				for _, at := range instants {
					wr, werr := lookupIn(want, indexFor(want, at), m.provider, m.model)
					gr, gerr := eng.LookupAt(m.provider, m.model, at)
					if gr != wr || (werr == nil) != (gerr == nil) || (werr != nil && werr.Error() != gerr.Error()) ||
						errors.Is(werr, ErrUnknownModel) != errors.Is(gerr, ErrUnknownModel) {
						t.Fatalf("%s: %s/%s at %s = %+v, %v; want %+v, %v", step, m.provider, m.model, at, gr, gerr, wr, werr)
					}
				}
			}
		}
	}
	check("first cards", memoCards(1))
	eng.Replace(memoCards(10))
	check("after Replace", memoCards(10))
	eng.Replace(memoCards(1)[:1])
	check("after a Replace with one card", memoCards(1)[:1])
}

// The memo is bounded: past its cap, answers are looked up and not kept.
func TestLookupAtMemoIsBounded(t *testing.T) {
	eng := NewDatedEngine(memoCards(1))
	for i := range rateMemoCap + 100 {
		_, _ = eng.LookupAt(eventschema.ProviderOpenAI, "model-"+string(rune('a'+i%26))+time.Duration(i).String(), time.Time{})
	}
	m := eng.rates.Load()
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.rates) > rateMemoCap {
		t.Fatalf("memo holds %d answers, cap %d", len(m.rates), rateMemoCap)
	}
}

// Pricing races a Replace: every price is the old card's or the new
// one's, and once the Replace is done, only the new one's.
func TestLookupAtMemoUnderReplace(t *testing.T) {
	eng := NewDatedEngine(memoCards(1))
	at := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				r, err := eng.LookupAt(eventschema.ProviderOpenAI, "gpt-5", at)
				if err != nil || (r.InputPerMillion != 3 && r.InputPerMillion != 30) {
					t.Errorf("priced at %+v, %v", r, err)
					return
				}
			}
		}()
	}
	for i := range 200 {
		eng.Replace(memoCards(float64(1 + 9*(i%2))))
	}
	eng.Replace(memoCards(10))
	close(stop)
	wg.Wait()
	if r, _ := eng.LookupAt(eventschema.ProviderOpenAI, "gpt-5", at); r.InputPerMillion != 30 {
		t.Fatalf("after the last Replace, priced at %+v", r)
	}
}
