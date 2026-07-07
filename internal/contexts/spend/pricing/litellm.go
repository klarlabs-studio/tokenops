package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DefaultLiteLLMURL is BerriAI/litellm's machine-readable rate card: vendor
// list prices (not proxy-marked-up), stable raw URL, no API key. This is the
// ADR 0002 default source.
const DefaultLiteLLMURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// litellmTimeout bounds the fetch so a hung endpoint can't stall a refresh.
const litellmTimeout = 10 * time.Second

// LiteLLMSource fetches and normalizes the LiteLLM rate card. The HTTP client
// is injectable so tests drive it against an httptest.Server (no live network
// in the suite); the zero-value client gets a bounded default.
type LiteLLMSource struct {
	URL    string
	Client *http.Client
}

// NewLiteLLMSource returns a source pointed at DefaultLiteLLMURL with a
// 10-second HTTP client.
func NewLiteLLMSource() *LiteLLMSource {
	return &LiteLLMSource{
		URL:    DefaultLiteLLMURL,
		Client: &http.Client{Timeout: litellmTimeout},
	}
}

// Name implements Source.
func (s *LiteLLMSource) Name() string { return "litellm" }

// litellmEntry is the subset of a LiteLLM model record this adapter reads.
// Costs are per single token; convert to per-million with ×1e6.
type litellmEntry struct {
	Provider          string  `json:"litellm_provider"`
	InputCostPerToken float64 `json:"input_cost_per_token"`
	OutputCost        float64 `json:"output_cost_per_token"`
	CacheReadCost     float64 `json:"cache_read_input_token_cost"`
	CacheCreationCost float64 `json:"cache_creation_input_token_cost"`
}

// perMillion converts a per-token cost to per-million tokens.
const perMillion = 1_000_000.0

// perMillionCost converts a per-token cost to per-million and rounds to 6
// decimal places (micro-dollars per million). Rounding removes the float
// noise of ×1e6 — e.g. 0.0000008 × 1e6 = 0.7999999999999999 — so an unchanged
// rate compares equal to the YAML baseline and does not manufacture a spurious
// diff line.
func perMillionCost(perToken float64) float64 {
	return math.Round(perToken*perMillion*1e6) / 1e6
}

// Fetch implements Source: GET the URL under ctx, parse the LiteLLM map, and
// map the SnapshotProvider (Anthropic) entries into a Snapshot. Any transport,
// status, or parse failure is wrapped in ErrFetch so the caller falls back to
// the baseline without writing a snapshot.
func (s *LiteLLMSource) Fetch(ctx context.Context) (Snapshot, error) {
	url := s.URL
	if url == "" {
		url = DefaultLiteLLMURL
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: litellmTimeout}
	}
	// Belt-and-braces deadline even if the injected client has no timeout.
	ctx, cancel := context.WithTimeout(ctx, litellmTimeout)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 32 MiB cap
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: read body: %v", ErrFetch, err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return Snapshot{}, fmt.Errorf("%w: parse JSON: %v", ErrFetch, err)
	}

	snap := Snapshot{
		Source:    s.Name(),
		SourceURL: url,
		FetchedAt: time.Now().UTC(),
		Rates:     map[string]Rate{},
	}
	catalogKeys := catalogModelKeys()

	// Sort ids for deterministic collision handling: when several dated
	// variants collapse to one tokenops key, the lexically-first wins.
	ids := make([]string, 0, len(raw))
	for id := range raw {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		if id == "sample_spec" { // LiteLLM's schema doc row, not a model
			continue
		}
		var e litellmEntry
		if json.Unmarshal(raw[id], &e) != nil {
			continue // non-model or unexpected shape → skip, don't fail
		}
		if !strings.EqualFold(e.Provider, string(SnapshotProvider)) {
			continue
		}
		if e.InputCostPerToken == 0 && e.OutputCost == 0 {
			continue // no usable price
		}
		key := mapModelKey(id, catalogKeys)
		if _, exists := snap.Rates[key]; exists {
			continue // keep first (lexically smallest id) on collision
		}
		snap.Rates[key] = Rate{
			InputPerMillion:       perMillionCost(e.InputCostPerToken),
			OutputPerMillion:      perMillionCost(e.OutputCost),
			CachedInputPerMillion: perMillionCost(e.CacheReadCost),
		}
	}
	return snap, nil
}

// catalogModelKeys returns the tokenops Anthropic model keys (from the
// embedded baseline), longest first, so mapModelKey can longest-prefix match.
func catalogModelKeys() []string {
	base := BaselineSnapshot()
	keys := make([]string, 0, len(base.Rates))
	for k := range base.Rates {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	return keys
}

// dateSuffix matches a trailing "-YYYYMMDD" or "-YYMMDD" date variant that
// LiteLLM appends to dated model snapshots (e.g. "-20241022").
var dateSuffix = regexp.MustCompile(`-\d{6,8}$`)

// mapModelKey maps a LiteLLM model id to a tokenops model key. It first takes
// the longest catalog key that is a prefix of the id (so
// "claude-3-5-sonnet-20241022" → "claude-3-5-sonnet"); when nothing matches it
// falls back to a normalized form of the id (date/`-latest` suffix stripped)
// so genuinely new models still surface in the diff under a readable key.
func mapModelKey(id string, catalogKeys []string) string {
	for _, k := range catalogKeys {
		if k != "" && strings.HasPrefix(id, k) {
			return k
		}
	}
	return normalizeModelID(id)
}

// normalizeModelID strips a leading "anthropic/" namespace and a trailing
// dated or "-latest" version marker, yielding a stable key for models absent
// from the catalog.
func normalizeModelID(id string) string {
	id = strings.TrimPrefix(id, "anthropic/")
	id = strings.TrimSuffix(id, "-latest")
	id = dateSuffix.ReplaceAllString(id, "")
	return strings.TrimSpace(id)
}
