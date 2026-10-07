package pricing

import (
	"context"
	"errors"
	"testing"
	"time"
)

const modelsDevSample = `{
  "fireworks-ai": {"models": {
    "accounts/fireworks/models/kimi-k3": {"cost": {"input": 3, "output": 15, "cache_read": 0.3}},
    "accounts/fireworks/routers/glm-fast-latest": {"cost": {"input": 2.1, "output": 6.6, "cache_read": 0.39}},
    "accounts/fireworks/models/free-thing": {}
  }},
  "openrouter": {"models": {"anthropic/claude-sonnet-5": {"cost": {"input": 3, "output": 15}}}},
  "anthropic": {"models": {"claude-sonnet-5": {"cost": {"input": 3, "output": 15}}}},
  "zai-coding-plan": {"models": {"glm-5.3": {"cost": {"input": 0, "output": 0}}}},
  "moonshotai": {"models": {"kimi-k3": {"cost": {"input": 3, "output": 15, "cache_read": 0.3}}}}
}`

func TestParseModelsDevPricesGatewaysOnly(t *testing.T) {
	snap, err := ParseModelsDev([]byte(modelsDevSample), "https://fixture.invalid/api.json", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Rate{
		"fireworks/kimi-k3":                    {InputPerMillion: 3, OutputPerMillion: 15, CachedInputPerMillion: 0.3},
		"fireworks/glm-fast-latest":            {InputPerMillion: 2.1, OutputPerMillion: 6.6, CachedInputPerMillion: 0.39},
		"openrouter/anthropic/claude-sonnet-5": {InputPerMillion: 3, OutputPerMillion: 15},
		// A coding plan's $0 is not a price; its turns take the vendor's rate.
		"moonshot/kimi-k3": {InputPerMillion: 3, OutputPerMillion: 15, CachedInputPerMillion: 0.3},
		"kimi/kimi-k3":     {InputPerMillion: 3, OutputPerMillion: 15, CachedInputPerMillion: 0.3},
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

type okSource struct{}

func (okSource) Name() string { return "up" }
func (okSource) Fetch(context.Context) (Snapshot, error) {
	return ParseModelsDev([]byte(modelsDevSample), "https://fixture.invalid/api.json", time.Now())
}

type failSource struct{}

func (failSource) Name() string { return "down" }
func (failSource) Fetch(context.Context) (Snapshot, error) {
	return Snapshot{}, ErrFetch
}

func TestCombinedFailsWholeWhenOneSourceFails(t *testing.T) {
	_, err := Combined{okSource{}, failSource{}}.Fetch(context.Background())
	if !errors.Is(err, ErrFetch) {
		t.Errorf("err = %v; a snapshot missing one source's rows must not be written", err)
	}
}

func TestParseModelsDevErrorsWrapped(t *testing.T) {
	for name, body := range map[string]string{
		"bad JSON":        "not json",
		"no gateway rate": `{"anthropic": {"models": {"claude-sonnet-5": {"cost": {"input": 3, "output": 15}}}}}`,
	} {
		if _, err := ParseModelsDev([]byte(body), "https://fixture.invalid/api.json", time.Now()); !errors.Is(err, ErrFetch) {
			t.Errorf("%s: err = %v, want ErrFetch", name, err)
		}
	}
}

func TestCombinedMergesSources(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	a := fixedSource{name: "a", snap: Snapshot{FetchedAt: at, Rates: map[string]Rate{"x/m": {InputPerMillion: 1}}}}
	b := fixedSource{name: "b", snap: Snapshot{FetchedAt: at.Add(time.Hour), Rates: map[string]Rate{"x/m": {InputPerMillion: 9}, "y/n": {InputPerMillion: 2}}}}
	snap, err := Combined{a, b}.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Source != "a+b" || !snap.FetchedAt.Equal(at.Add(time.Hour)) {
		t.Errorf("provenance = %q @ %v", snap.Source, snap.FetchedAt)
	}
	if snap.Rates["x/m"].InputPerMillion != 1 || snap.Rates["y/n"].InputPerMillion != 2 {
		t.Errorf("rates = %v; the first source keeps a shared key", snap.Rates)
	}
}

type fixedSource struct {
	name string
	snap Snapshot
}

func (f fixedSource) Name() string                            { return f.name }
func (f fixedSource) Fetch(context.Context) (Snapshot, error) { return f.snap, nil }
