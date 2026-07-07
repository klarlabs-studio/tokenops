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
	"strconv"
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

// litellmProviderMap maps a LiteLLM "litellm_provider" value to the tokenops
// provider string used in the catalog. Only providers the embedded catalog can
// price are listed; an entry whose litellm_provider is absent here is skipped,
// so the fetched snapshot's key-space stays aligned with the baseline and the
// diff remains meaningful. Multiplexers (fireworks, together, openrouter) are
// intentionally excluded — a static card cannot price their namespaced models.
var litellmProviderMap = map[string]string{
	"anthropic":                 "anthropic",
	"openai":                    "openai",
	"text-completion-openai":    "openai",
	"mistral":                   "mistral",
	"gemini":                    "gemini",
	"vertex_ai":                 "gemini",
	"vertex_ai-language-models": "gemini",
	"google":                    "gemini",
	"cohere":                    "cohere",
	"cohere_chat":               "cohere",
	"groq":                      "groq",
	"deepseek":                  "deepseek",
	"xai":                       "xai",
	"perplexity":                "perplexity",
	"cerebras":                  "cerebras",
}

// vendorPrefixes are the leading "<vendor>/" namespaces LiteLLM prepends to
// some model ids (e.g. "mistral/mistral-large-latest", "vertex_ai/gemini-…").
// Only these known tokens are stripped, so multi-segment ids from unmapped
// multiplexers are never mangled.
var vendorPrefixes = map[string]bool{
	"anthropic": true, "openai": true, "mistral": true, "gemini": true,
	"vertex_ai": true, "google": true, "cohere": true, "groq": true,
	"deepseek": true, "xai": true, "perplexity": true, "cerebras": true,
}

// Fetch implements Source: GET the URL under ctx, parse the LiteLLM map, and
// map every entry whose litellm_provider resolves to a catalog provider into a
// Snapshot keyed "<provider>/<model>". Any transport, status, or parse failure
// is wrapped in ErrFetch so the caller falls back to the baseline without
// writing a snapshot.
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

	// Group candidate LiteLLM ids by the catalog key they map to. A broad
	// catalog key (e.g. "mistral-large") collapses every dated SKU LiteLLM
	// carries — mistral-large-2402, -2411, -latest, … — onto one key. Picking
	// the lexically-first id would adopt the *oldest* archived snapshot (usually
	// the priciest), manufacturing false "drift". Instead pick the id that best
	// represents the vendor's live price via preferID (see versionRank).
	type candidate struct {
		id   string
		rate Rate
	}
	groups := make(map[string][]candidate)
	for id, msg := range raw {
		if id == "sample_spec" { // LiteLLM's schema doc row, not a model
			continue
		}
		var e litellmEntry
		if json.Unmarshal(msg, &e) != nil {
			continue // non-model or unexpected shape → skip, don't fail
		}
		provider, ok := litellmProviderMap[strings.ToLower(e.Provider)]
		if !ok {
			continue // provider not in the catalog → skip so key-space matches
		}
		providerKeys, priced := catalogKeys[provider]
		if !priced {
			continue // catalog carries no rows for this provider
		}
		if e.InputCostPerToken == 0 && e.OutputCost == 0 {
			continue // no usable price
		}
		key := snapKey(provider, mapModelKey(id, providerKeys))
		groups[key] = append(groups[key], candidate{
			id: id,
			rate: Rate{
				InputPerMillion:       perMillionCost(e.InputCostPerToken),
				OutputPerMillion:      perMillionCost(e.OutputCost),
				CachedInputPerMillion: perMillionCost(e.CacheReadCost),
			},
		})
	}
	for key, cands := range groups {
		best := cands[0]
		for _, c := range cands[1:] {
			if preferID(c.id, best.id) {
				best = c
			}
		}
		snap.Rates[key] = best.rate
	}
	return snap, nil
}

// versionDate matches a trailing numeric version/date suffix LiteLLM appends to
// dated model snapshots: 8-digit YYYYMMDD (Anthropic "claude-…-20241022"),
// 6-digit YYMMDD, or 4-digit YYMM (Mistral "mistral-large-2411"). Higher digits
// mean a newer release, so the captured number orders variants within a family.
var versionDate = regexp.MustCompile(`-(\d{4,8})$`)

// versionRank scores how well a LiteLLM model id represents the *current* price
// of its family. Higher wins: a precise dated snapshot outranks everything and
// the newest date among them wins; a "-latest" alias is the fallback used only
// when no dated SKU is listed; an undated/bare id ranks lowest. Dated snapshots
// are preferred over "-latest" because LiteLLM's "-latest" aliases are sometimes
// stale — e.g. "codestral-latest" still carries the old $1/$3 rate while the
// current "codestral-2508" is $0.30/$0.90 — so trusting the alias would
// manufacture false drift against a catalog that tracks the newest SKU.
func versionRank(id string) (tier, date int) {
	stem := stripVendorPrefix(id)
	switch {
	case versionDate.MatchString(stem):
		n, _ := strconv.Atoi(versionDate.FindStringSubmatch(stem)[1])
		return 3, n // a precise dated snapshot — newest wins
	case strings.HasSuffix(stem, "-latest"):
		return 2, 0 // vendor's moving pointer; used when no dated SKU is listed
	default:
		return 1, 0 // undated / bare id
	}
}

// preferID reports whether LiteLLM id a is a better current-price representative
// than b for a colliding catalog key (see versionRank). Ties break to the
// lexically-smaller id so a refresh is deterministic across runs.
func preferID(a, b string) bool {
	at, ad := versionRank(a)
	bt, bd := versionRank(b)
	switch {
	case at != bt:
		return at > bt
	case ad != bd:
		return ad > bd
	default:
		return a < b
	}
}

// catalogModelKeys returns the tokenops catalog model keys (from the embedded
// baseline) grouped by provider, each group sorted longest-first so mapModelKey
// can longest-prefix match within the right provider.
func catalogModelKeys() map[string][]string {
	base := BaselineSnapshot()
	out := make(map[string][]string)
	for key := range base.Rates {
		provider, model := splitSnapKey(key)
		out[provider] = append(out[provider], model)
	}
	for _, keys := range out {
		sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	}
	return out
}

// dateSuffix matches a trailing "-YYYYMMDD" or "-YYMMDD" date variant that
// LiteLLM appends to dated model snapshots (e.g. "-20241022").
var dateSuffix = regexp.MustCompile(`-\d{6,8}$`)

// mapModelKey maps a LiteLLM model id to a tokenops model key within its
// provider. It strips any leading "<vendor>/" namespace, then takes the longest
// catalog key that is a prefix of the result (so "mistral/mistral-large-latest"
// → "mistral-large" and "claude-3-5-sonnet-20241022" → "claude-3-5-sonnet");
// when nothing matches it falls back to a normalized form of the id (vendor
// prefix + date/`-latest` marker stripped) so genuinely new models still
// surface in the diff under a readable key.
func mapModelKey(id string, catalogKeys []string) string {
	stem := stripVendorPrefix(id)
	for _, k := range catalogKeys {
		if k != "" && strings.HasPrefix(stem, k) {
			return k
		}
	}
	return normalizeModelID(id)
}

// stripVendorPrefix removes a single leading "<vendor>/" namespace when the
// vendor is a known token, leaving multi-segment ids from unmapped
// multiplexers untouched.
func stripVendorPrefix(id string) string {
	if i := strings.Index(id, "/"); i > 0 && vendorPrefixes[strings.ToLower(id[:i])] {
		return id[i+1:]
	}
	return id
}

// normalizeModelID strips a leading "<vendor>/" namespace and a trailing dated
// or "-latest" version marker, yielding a stable key for models absent from
// the catalog.
func normalizeModelID(id string) string {
	id = stripVendorPrefix(id)
	id = strings.TrimSuffix(id, "-latest")
	id = dateSuffix.ReplaceAllString(id, "")
	return strings.TrimSpace(id)
}
