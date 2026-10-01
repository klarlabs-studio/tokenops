package pricing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const modelsDevSample = `{
  "fireworks-ai": {"models": {
    "accounts/fireworks/models/kimi-k3": {"cost": {"input": 3, "output": 15, "cache_read": 0.3}},
    "accounts/fireworks/routers/glm-fast-latest": {"cost": {"input": 2.1, "output": 6.6, "cache_read": 0.39}},
    "accounts/fireworks/models/free-thing": {}
  }},
  "openrouter": {"models": {"anthropic/claude-sonnet-5": {"cost": {"input": 3, "output": 15}}}},
  "anthropic": {"models": {"claude-sonnet-5": {"cost": {"input": 3, "output": 15}}}}
}`

func TestModelsDevSourcePricesGatewaysOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(modelsDevSample))
	}))
	defer srv.Close()
	snap, err := (&ModelsDevSource{URL: srv.URL}).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Rate{
		"fireworks/kimi-k3":                    {InputPerMillion: 3, OutputPerMillion: 15, CachedInputPerMillion: 0.3},
		"fireworks/glm-fast-latest":            {InputPerMillion: 2.1, OutputPerMillion: 6.6, CachedInputPerMillion: 0.39},
		"openrouter/anthropic/claude-sonnet-5": {InputPerMillion: 3, OutputPerMillion: 15},
	}
	if len(snap.Rates) != len(want) {
		t.Fatalf("rates %v; want only the gateways' priced models (a vendor is LiteLLM's)", snap.Rates)
	}
	for k, r := range want {
		if snap.Rates[k] != r {
			t.Errorf("%s = %+v, want %+v", k, snap.Rates[k], r)
		}
	}
}

type failSource struct{}

func (failSource) Name() string { return "down" }
func (failSource) Fetch(context.Context) (Snapshot, error) {
	return Snapshot{}, ErrFetch
}

func TestCombinedFailsWholeWhenOneSourceFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(modelsDevSample))
	}))
	defer srv.Close()
	_, err := Combined{&ModelsDevSource{URL: srv.URL}, failSource{}}.Fetch(context.Background())
	if !errors.Is(err, ErrFetch) {
		t.Errorf("err = %v; a snapshot missing one source's rows must not be written", err)
	}
}
