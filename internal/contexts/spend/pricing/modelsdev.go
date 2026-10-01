package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultModelsDevURL is models.dev's catalog: per-provider model lists
// with each provider's own price, the data opencode prices from.
const DefaultModelsDevURL = "https://models.dev/api.json"

const modelsDevTimeout = 20 * time.Second

// modelsDevProviders maps a models.dev provider ID to the TokenOps
// providers its rates price (ADR 0009 §6). LiteLLM covers the major model
// vendors; models.dev covers gateways and the vendors that sell coding
// plans. A coding-plan ID (zai-coding-plan, kimi-code-plan-global, …) is
// listed there at $0, true of the plan and useless as a price, so it is
// not mapped: plan turns are valued at the vendor's pay-as-you-go rate,
// which is why moonshotai also prices Kimi Code ("kimi").
var modelsDevProviders = map[string][]string{
	"fireworks-ai": {"fireworks"},
	"openrouter":   {"openrouter"},
	"togetherai":   {"together"},
	"zai":          {"zai"},
	"zhipuai":      {"zhipuai"},
	"moonshotai":   {"moonshot", "kimi"},
	"minimax":      {"minimax"},
	"alibaba":      {"alibaba"},
	"opencode":     {"opencode"},
	"opencode-go":  {"opencode-go"},
	"chutes":       {"chutes"},
	"synthetic":    {"synthetic"},
}

// ModelsDevSource fetches gateway rates from models.dev.
type ModelsDevSource struct {
	URL    string
	Client *http.Client
}

// NewModelsDevSource returns a source pointed at the public catalog.
func NewModelsDevSource() *ModelsDevSource { return &ModelsDevSource{URL: DefaultModelsDevURL} }

// Name implements Source.
func (s *ModelsDevSource) Name() string { return "models.dev" }

// Fetch implements Source.
func (s *ModelsDevSource) Fetch(ctx context.Context) (Snapshot, error) {
	url := s.URL
	if url == "" {
		url = DefaultModelsDevURL
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: modelsDevTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, modelsDevTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: build request: %v", ErrFetch, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: GET %s: %v", ErrFetch, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Snapshot{}, fmt.Errorf("%w: GET %s: status %d", ErrFetch, url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: read body: %v", ErrFetch, err)
	}
	var catalog map[string]struct {
		Models map[string]struct {
			Cost *struct {
				Input     float64 `json:"input"`
				Output    float64 `json:"output"`
				CacheRead float64 `json:"cache_read"`
			} `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		return Snapshot{}, fmt.Errorf("%w: parse JSON: %v", ErrFetch, err)
	}
	snap := Snapshot{Source: s.Name(), SourceURL: url, FetchedAt: time.Now().UTC(), Rates: map[string]Rate{}}
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
				}
			}
		}
	}
	if len(snap.Rates) == 0 {
		return Snapshot{}, fmt.Errorf("%w: %s carried no gateway rates", ErrFetch, url)
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
