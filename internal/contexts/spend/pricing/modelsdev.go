package pricing

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
)

// ModelsDevSourceName is the Source name, and Snapshot.Source, of models.dev's
// catalog: per-provider model lists with each provider's own price, the data
// opencode prices from. The HTTP fetch lives in internal/infra/pricingsource.
const ModelsDevSourceName = "models.dev"

// modelsDevProviders maps a models.dev provider ID to the TokenOps
// providers its rates price (ADR 0009 §6), from each provider descriptor's
// ModelsDev IDs. LiteLLM covers the major model
// vendors; models.dev covers gateways and the vendors that sell coding
// plans. A coding-plan ID (zai-coding-plan, kimi-code-plan-global, …) is
// listed there at $0, true of the plan and useless as a price, so it is
// not mapped: plan turns are valued at the vendor's pay-as-you-go rate,
// which is why moonshotai also prices Kimi Code ("kimi").
var modelsDevProviders = providers.ModelsDevPricing()

// ParseModelsDev normalizes a models.dev catalog body into a Snapshot of the
// gateway rates modelsDevProviders maps. sourceURL and fetchedAt are the
// fetch's provenance. A body that does not parse, or that carries no gateway
// rate at all, is wrapped in ErrFetch.
func ParseModelsDev(body []byte, sourceURL string, fetchedAt time.Time) (Snapshot, error) {
	var catalog map[string]struct {
		Models map[string]struct {
			Cost *struct {
				Input      float64 `json:"input"`
				Output     float64 `json:"output"`
				CacheRead  float64 `json:"cache_read"`
				CacheWrite float64 `json:"cache_write"`
			} `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		return Snapshot{}, fmt.Errorf("%w: parse JSON: %v", ErrFetch, err)
	}
	snap := Snapshot{Source: ModelsDevSourceName, SourceURL: sourceURL, FetchedAt: fetchedAt, Rates: map[string]Rate{}}
	for id, p := range catalog {
		providers, ok := modelsDevProviders[id]
		if !ok {
			continue
		}
		for modelID, m := range p.Models {
			if m.Cost == nil || (m.Cost.Input <= 0 && m.Cost.Output <= 0) {
				continue
			}
			for _, provider := range providers {
				snap.Rates[snapKey(provider, GatewayModelID(modelID))] = Rate{
					InputPerMillion:       m.Cost.Input,
					OutputPerMillion:      m.Cost.Output,
					CachedInputPerMillion: m.Cost.CacheRead,
					CacheWritePerMillion:  m.Cost.CacheWrite,
				}
			}
		}
	}
	if len(snap.Rates) == 0 {
		return Snapshot{}, fmt.Errorf("%w: %s carried no gateway rates", ErrFetch, sourceURL)
	}
	return snap, nil
}

// GatewayModelID drops a gateway's account namespace from a model ID:
// "accounts/fireworks/models/kimi-k3" and "accounts/fireworks/routers/
// kimi-latest" become "kimi-k3" and "kimi-latest", the names a harness
// records once the namespace is stripped.
func GatewayModelID(id string) string {
	parts := strings.Split(id, "/")
	if len(parts) == 4 && parts[0] == "accounts" && (parts[2] == "models" || parts[2] == "routers") {
		return parts[3]
	}
	return id
}
