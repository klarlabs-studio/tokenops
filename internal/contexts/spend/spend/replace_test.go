package spend

import (
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func oneTable(model string, in float64) Table {
	return Table{Currency: "USD", Rates: map[Key]Rate{
		{Provider: eventschema.ProviderOpenAI, Model: model}: {InputPerMillion: in, OutputPerMillion: in},
	}}
}

// A daemon that refreshes its rate card must actually price from the new
// one. Writing a snapshot the live process never reads is a no-op wearing
// the clothes of an update.
func TestReplaceSwapsTheLiveRateCard(t *testing.T) {
	eng := NewEngine(oneTable("old-model", 1))
	if _, err := eng.Table().Lookup(eventschema.ProviderOpenAI, "new-model"); err == nil {
		t.Fatal("new-model priced before the refresh")
	}
	eng.Replace([]DatedTable{{EffectiveFrom: time.Time{}, Table: oneTable("new-model", 2)}})
	if _, err := eng.Table().Lookup(eventschema.ProviderOpenAI, "new-model"); err != nil {
		t.Errorf("new-model still unpriced after Replace: %v", err)
	}
}

// Losing every rate is strictly worse than keeping a stale one, so an
// empty set is ignored rather than installed.
func TestReplaceIgnoresAnEmptyCard(t *testing.T) {
	eng := NewEngine(oneTable("keep-me", 1))
	eng.Replace(nil)
	if _, err := eng.Table().Lookup(eventschema.ProviderOpenAI, "keep-me"); err != nil {
		t.Errorf("an empty Replace wiped the rate card: %v", err)
	}
}

// The proxy prices requests on other goroutines while the refresh loop
// swaps the card. A reader sees the old card or the new one, never a
// half-applied mix.
func TestReplaceIsSafeUnderConcurrentPricing(t *testing.T) {
	eng := NewEngine(oneTable("m", 1))
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
					_, _ = eng.ComputeAt(&eventschema.PromptEvent{
						Provider: eventschema.ProviderOpenAI, RequestModel: "m",
						InputTokens: 1000, CostSource: eventschema.CostSourceMetered,
					}, time.Now())
				}
			}
		}()
	}
	for i := range 50 {
		eng.Replace([]DatedTable{{Table: oneTable("m", float64(i+1))}})
	}
	close(stop)
	wg.Wait()
}
