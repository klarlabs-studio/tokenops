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

func TestLiteLLMSource_FetchMapsAnthropicOnly(t *testing.T) {
	srv := fixtureServer(t)
	src := &LiteLLMSource{URL: srv.URL, Client: srv.Client()}

	snap, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if snap.Source != "litellm" || snap.SourceURL != srv.URL {
		t.Errorf("provenance = %q/%q", snap.Source, snap.SourceURL)
	}
	// openai gpt-4o and the sample_spec row must be filtered out.
	if _, ok := snap.Rates["gpt-4o"]; ok {
		t.Error("non-anthropic model leaked into snapshot")
	}
	if _, ok := snap.Rates["sample_spec"]; ok {
		t.Error("sample_spec doc row leaked into snapshot")
	}
}

func TestLiteLLMSource_PerTokenToPerMillion(t *testing.T) {
	srv := fixtureServer(t)
	src := &LiteLLMSource{URL: srv.URL, Client: srv.Client()}
	snap, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	opus, ok := snap.Rates["claude-opus-4-8"]
	if !ok {
		t.Fatalf("opus not mapped; keys=%v", snap.Models())
	}
	// 0.000015 per token → 15 per million.
	if opus.InputPerMillion != 15 || opus.OutputPerMillion != 75 || opus.CachedInputPerMillion != 1.5 {
		t.Errorf("opus per-million = %+v, want 15/75/1.5", opus)
	}
}

func TestLiteLLMSource_KeyMapping(t *testing.T) {
	srv := fixtureServer(t)
	src := &LiteLLMSource{URL: srv.URL, Client: srv.Client()}
	snap, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Dated ids collapse to the catalog prefix key.
	if _, ok := snap.Rates["claude-3-5-sonnet"]; !ok {
		t.Errorf("dated sonnet not mapped to catalog key; keys=%v", snap.Models())
	}
	if _, ok := snap.Rates["claude-3-5-haiku"]; !ok {
		t.Errorf("dated haiku not mapped to catalog key; keys=%v", snap.Models())
	}
	// A model with no catalog key surfaces under its normalized id (date stripped).
	if _, ok := snap.Rates["claude-3-opus"]; !ok {
		t.Errorf("new model should surface under normalized key; keys=%v", snap.Models())
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
