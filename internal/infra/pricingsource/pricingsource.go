// Package pricingsource fetches the machine-readable price catalogs over
// HTTP and hands their bodies to the spend/pricing domain to normalize. It
// implements pricing.Source; the domain keeps the snapshot model, the diff,
// the consistency guard and effective-dating, and does no I/O itself.
package pricingsource

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
)

// DefaultLiteLLMURL is BerriAI/litellm's machine-readable rate card: vendor
// list prices (not proxy-marked-up), stable raw URL, no API key. This is the
// ADR 0002 default source.
const DefaultLiteLLMURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// DefaultModelsDevURL is models.dev's catalog: per-provider model lists
// with each provider's own price, the data opencode prices from.
const DefaultModelsDevURL = "https://models.dev/api.json"

const (
	// litellmTimeout bounds the fetch so a hung endpoint can't stall a refresh.
	litellmTimeout   = 10 * time.Second
	modelsDevTimeout = 20 * time.Second
	// maxBody caps a catalog body at 32 MiB.
	maxBody = 32 << 20
)

// LiteLLM fetches the LiteLLM rate card. The HTTP client is injectable so
// tests drive it against an httptest.Server (no live network in the suite);
// a nil client gets a bounded default.
type LiteLLM struct {
	URL    string
	Client *http.Client
}

// NewLiteLLM returns a source pointed at DefaultLiteLLMURL with a 10-second
// HTTP client.
func NewLiteLLM() *LiteLLM {
	return &LiteLLM{
		URL:    DefaultLiteLLMURL,
		Client: &http.Client{Timeout: litellmTimeout},
	}
}

// Name implements pricing.Source.
func (s *LiteLLM) Name() string { return pricing.LiteLLMSourceName }

// Fetch implements pricing.Source: GET the URL under ctx and normalize the
// body with pricing.ParseLiteLLM. Any transport, status, or parse failure is
// wrapped in pricing.ErrFetch so the caller falls back to the baseline
// without writing a snapshot.
func (s *LiteLLM) Fetch(ctx context.Context) (pricing.Snapshot, error) {
	url := s.URL
	if url == "" {
		url = DefaultLiteLLMURL
	}
	body, err := get(ctx, s.Client, url, litellmTimeout)
	if err != nil {
		return pricing.Snapshot{}, err
	}
	return pricing.ParseLiteLLM(body, url, time.Now().UTC())
}

// ModelsDev fetches gateway rates from models.dev.
type ModelsDev struct {
	URL    string
	Client *http.Client
}

// NewModelsDev returns a source pointed at the public catalog.
func NewModelsDev() *ModelsDev { return &ModelsDev{URL: DefaultModelsDevURL} }

// Name implements pricing.Source.
func (s *ModelsDev) Name() string { return pricing.ModelsDevSourceName }

// Fetch implements pricing.Source: GET the catalog under ctx and normalize
// it with pricing.ParseModelsDev.
func (s *ModelsDev) Fetch(ctx context.Context) (pricing.Snapshot, error) {
	url := s.URL
	if url == "" {
		url = DefaultModelsDevURL
	}
	body, err := get(ctx, s.Client, url, modelsDevTimeout)
	if err != nil {
		return pricing.Snapshot{}, err
	}
	return pricing.ParseModelsDev(body, url, time.Now().UTC())
}

// get fetches url and returns its body, every failure wrapped in
// pricing.ErrFetch. timeout bounds the request even when the injected
// client has none.
func get(ctx context.Context, client *http.Client, url string, timeout time.Duration) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", pricing.ErrFetch, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: GET %s: %v", pricing.ErrFetch, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: GET %s: status %d", pricing.ErrFetch, url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", pricing.ErrFetch, err)
	}
	return body, nil
}

// ByName returns the built-in Source for name, or nil when unknown:
// "litellm", "models.dev", or "default" (both, combined). url overrides the
// source's default endpoint when non-empty (used for `--url` and, in tests,
// an httptest server).
func ByName(name, url string) pricing.Source {
	switch name {
	case "litellm":
		s := NewLiteLLM()
		if url != "" {
			s.URL = url
		}
		return s
	case "models.dev", "modelsdev":
		s := NewModelsDev()
		if url != "" {
			s.URL = url
		}
		return s
	case "", "default":
		// A URL names one endpoint, so it can only stand in for one
		// source: LiteLLM, which "default" meant before models.dev.
		if url != "" {
			return ByName("litellm", url)
		}
		return pricing.Combined{NewLiteLLM(), NewModelsDev()}
	default:
		return nil
	}
}
