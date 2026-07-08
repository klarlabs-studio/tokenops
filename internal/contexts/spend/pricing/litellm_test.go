package pricing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fixtureServer serves the trimmed LiteLLM sample from testdata over httptest,
// so the suite never touches the live network.
func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "litellm_sample.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLiteLLMSource_MapsEveryCatalogProvider(t *testing.T) {
	srv := fixtureServer(t)
	src := &LiteLLMSource{URL: srv.URL, Client: srv.Client()}

	snap, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if snap.Source != "litellm" || snap.SourceURL != srv.URL {
		t.Errorf("provenance = %q/%q", snap.Source, snap.SourceURL)
	}
	// Anthropic, OpenAI, and Mistral entries all map to their tokenops
	// provider under a "<provider>/<model>" key.
	for _, want := range []string{
		"anthropic/claude-opus-4-8",
		"openai/gpt-4o",
		"mistral/mistral-large",
	} {
		if _, ok := snap.Rates[want]; !ok {
			t.Errorf("missing %q; keys=%v", want, snap.Models())
		}
	}
	// The sample_spec doc row and an unmapped provider (fireworks_ai) are
	// filtered out so the key-space matches the baseline.
	if _, ok := snap.Rates["sample_spec"]; ok {
		t.Error("sample_spec doc row leaked into snapshot")
	}
	for k := range snap.Rates {
		if provider, _ := splitSnapKey(k); provider == "fireworks_ai" || provider == "" {
			t.Errorf("unmapped/unqualified provider leaked: %q", k)
		}
	}
}

func TestLiteLLMSource_PerTokenToPerMillion(t *testing.T) {
	srv := fixtureServer(t)
	src := &LiteLLMSource{URL: srv.URL, Client: srv.Client()}
	snap, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	opus, ok := snap.Rates["anthropic/claude-opus-4-8"]
	if !ok {
		t.Fatalf("opus not mapped; keys=%v", snap.Models())
	}
	// 0.000005 per token → 5 per million.
	if opus.InputPerMillion != 5 || opus.OutputPerMillion != 25 || opus.CachedInputPerMillion != 0.5 {
		t.Errorf("opus per-million = %+v, want 5/25/0.5", opus)
	}
	// OpenAI: 0.0000025 → 2.5 per million.
	gpt, ok := snap.Rates["openai/gpt-4o"]
	if !ok {
		t.Fatalf("gpt-4o not mapped; keys=%v", snap.Models())
	}
	if gpt.InputPerMillion != 2.5 || gpt.OutputPerMillion != 10 || gpt.CachedInputPerMillion != 1.25 {
		t.Errorf("gpt-4o per-million = %+v, want 2.5/10/1.25", gpt)
	}
	// Mistral: 0.0000005 → 0.5 per million, no cache.
	mistral, ok := snap.Rates["mistral/mistral-large"]
	if !ok {
		t.Fatalf("mistral-large not mapped; keys=%v", snap.Models())
	}
	if mistral.InputPerMillion != 0.5 || mistral.OutputPerMillion != 1.5 {
		t.Errorf("mistral-large per-million = %+v, want 0.5/1.5", mistral)
	}
}

func TestLiteLLMSource_KeyMapping(t *testing.T) {
	srv := fixtureServer(t)
	src := &LiteLLMSource{URL: srv.URL, Client: srv.Client()}
	snap, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Dated ids collapse to the catalog prefix key (provider-qualified).
	if _, ok := snap.Rates["anthropic/claude-3-5-sonnet"]; !ok {
		t.Errorf("dated sonnet not mapped to catalog key; keys=%v", snap.Models())
	}
	if _, ok := snap.Rates["anthropic/claude-3-5-haiku"]; !ok {
		t.Errorf("dated haiku not mapped to catalog key; keys=%v", snap.Models())
	}
	// A "<vendor>/<model>-latest" id strips both prefix and suffix.
	if _, ok := snap.Rates["mistral/mistral-large"]; !ok {
		t.Errorf("mistral-large-latest not normalized; keys=%v", snap.Models())
	}
	// A model with no catalog key surfaces under its normalized id (date stripped).
	if _, ok := snap.Rates["anthropic/claude-3-opus"]; !ok {
		t.Errorf("new model should surface under normalized key; keys=%v", snap.Models())
	}
}

func TestPreferID_CurrentPriceWins(t *testing.T) {
	cases := []struct {
		name, a, b string
	}{
		{"newer date beats older", "mistral-large-2411", "mistral-large-2402"},
		{"dated beats latest (stale alias)", "codestral-2508", "codestral-latest"},
		{"dated beats bare", "mistral-medium-2505", "mistral-medium"},
		{"latest beats bare", "mistral-medium-latest", "mistral-medium"},
		{"8-digit newer beats older", "claude-3-5-sonnet-20241022", "claude-3-5-sonnet-20240620"},
		{"vendor prefix ignored", "mistral/mistral-large-2411", "mistral/mistral-large-2402"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !preferID(c.a, c.b) {
				t.Errorf("preferID(%q, %q) = false, want true", c.a, c.b)
			}
			if preferID(c.b, c.a) {
				t.Errorf("preferID is not antisymmetric for %q vs %q", c.a, c.b)
			}
		})
	}
}

// When several dated SKUs collapse onto one catalog key, the fetched rate must
// be the newest dated snapshot, not the oldest archived one nor a stale
// "-latest" alias. The fixture's mistral-large-2402 ($4/$12) and -latest ($3/$9)
// must lose to the newest dated -2411 ($0.50/$1.50, the current Large 3 rate).
func TestLiteLLMSource_CollisionAdoptsCurrentRate(t *testing.T) {
	srv := fixtureServer(t)
	src := &LiteLLMSource{URL: srv.URL, Client: srv.Client()}
	snap, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	large, ok := snap.Rates["mistral/mistral-large"]
	if !ok {
		t.Fatalf("mistral-large not mapped; keys=%v", snap.Models())
	}
	if large.InputPerMillion != 0.5 || large.OutputPerMillion != 1.5 {
		t.Errorf("mistral-large = %+v, want the newest-dated rate 0.5/1.5 (not an archived SKU)", large)
	}
}

func TestLiteLLMSource_FetchedSnapshotPassesGuard(t *testing.T) {
	srv := fixtureServer(t)
	src := &LiteLLMSource{URL: srv.URL, Client: srv.Client()}
	snap, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a := Check(snap); len(a) != 0 {
		t.Errorf("clean fixture flagged by guard: %v", a)
	}
}

func TestLiteLLMSource_DiffVsBaselineIsClean(t *testing.T) {
	srv := fixtureServer(t)
	src := &LiteLLMSource{URL: srv.URL, Client: srv.Client()}
	snap, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's overlapping models match the corrected baseline, so the
	// only diffs should be additions (claude-3-opus), never a modification of
	// an existing rate.
	for _, c := range Diff(BaselineSnapshot(), snap) {
		if c.Kind == ChangeModified {
			t.Errorf("unexpected rate change vs baseline: %s", FormatChange(c))
		}
	}
}

func TestLiteLLMSource_FetchErrorWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	src := &LiteLLMSource{URL: srv.URL, Client: srv.Client()}
	if _, err := src.Fetch(context.Background()); !errors.Is(err, ErrFetch) {
		t.Errorf("status 500 should wrap ErrFetch, got %v", err)
	}
}

func TestLiteLLMSource_BadJSONWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	src := &LiteLLMSource{URL: srv.URL, Client: srv.Client()}
	if _, err := src.Fetch(context.Background()); !errors.Is(err, ErrFetch) {
		t.Errorf("bad JSON should wrap ErrFetch, got %v", err)
	}
}

func TestSourceByName(t *testing.T) {
	if s := SourceByName("litellm", "http://x"); s == nil || s.Name() != "litellm" {
		t.Error("litellm source not returned")
	}
	if s := SourceByName("", ""); s == nil {
		t.Error("empty name should default to litellm")
	}
	if s := SourceByName("nope", ""); s != nil {
		t.Error("unknown source should be nil")
	}
	// --url override propagates.
	ls, ok := SourceByName("litellm", "http://override").(*LiteLLMSource)
	if !ok || ls.URL != "http://override" {
		t.Error("url override not applied")
	}
}
