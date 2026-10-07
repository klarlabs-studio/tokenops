package pricingsource

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
)

// serve answers every request with status and body, so the suite never
// touches the live network.
func serve(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func litellmFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "contexts", "spend", "pricing", "testdata", "litellm_sample.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

func TestLiteLLMFetchNormalizesBody(t *testing.T) {
	srv := serve(t, http.StatusOK, litellmFixture(t))
	snap, err := (&LiteLLM{URL: srv.URL, Client: srv.Client()}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if snap.Source != "litellm" || snap.SourceURL != srv.URL {
		t.Errorf("provenance = %q/%q", snap.Source, snap.SourceURL)
	}
	if snap.FetchedAt.IsZero() {
		t.Error("FetchedAt not stamped")
	}
	if _, ok := snap.Rates["anthropic/claude-opus-4-8"]; !ok {
		t.Errorf("fixture not normalized; keys=%v", snap.Models())
	}
}

func TestFetchErrorsWrapErrFetch(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "status 500", status: http.StatusInternalServerError},
		{name: "bad JSON", status: http.StatusOK, body: "not json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serve(t, tc.status, []byte(tc.body))
			for _, src := range []pricing.Source{
				&LiteLLM{URL: srv.URL, Client: srv.Client()},
				&ModelsDev{URL: srv.URL},
			} {
				if _, err := src.Fetch(context.Background()); !errors.Is(err, pricing.ErrFetch) {
					t.Errorf("%s: err = %v, want ErrFetch", src.Name(), err)
				}
			}
		})
	}
}

func TestFetchUnreachableWrapsErrFetch(t *testing.T) {
	srv := serve(t, http.StatusOK, nil)
	url := srv.URL
	srv.Close()
	if _, err := (&LiteLLM{URL: url}).Fetch(context.Background()); !errors.Is(err, pricing.ErrFetch) {
		t.Errorf("unreachable endpoint: err = %v, want ErrFetch", err)
	}
}

func TestModelsDevFetchNormalizesBody(t *testing.T) {
	srv := serve(t, http.StatusOK, []byte(`{"openrouter": {"models": {"anthropic/claude-sonnet-5": {"cost": {"input": 3, "output": 15}}}}}`))
	snap, err := (&ModelsDev{URL: srv.URL}).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Source != "models.dev" || snap.SourceURL != srv.URL {
		t.Errorf("provenance = %q/%q", snap.Source, snap.SourceURL)
	}
	if r := snap.Rates["openrouter/anthropic/claude-sonnet-5"]; r.InputPerMillion != 3 || r.OutputPerMillion != 15 {
		t.Errorf("rates = %v", snap.Rates)
	}
}

func TestByName(t *testing.T) {
	if s := ByName("litellm", "http://x"); s == nil || s.Name() != "litellm" {
		t.Error("litellm source not returned")
	}
	if s := ByName("models.dev", ""); s == nil || s.Name() != "models.dev" {
		t.Error("models.dev source not returned")
	}
	if s := ByName("", ""); s == nil || s.Name() != "litellm+models.dev" {
		t.Errorf("empty name should default to litellm+models.dev, got %v", s)
	}
	if s := ByName("nope", ""); s != nil {
		t.Error("unknown source should be nil")
	}
	// --url override propagates.
	ls, ok := ByName("litellm", "http://override").(*LiteLLM)
	if !ok || ls.URL != "http://override" {
		t.Error("url override not applied")
	}
	// A URL names one endpoint, so "default" with a URL is LiteLLM alone.
	if ds, ok := ByName("default", "http://override").(*LiteLLM); !ok || ds.URL != "http://override" {
		t.Error("default with url should be the LiteLLM source")
	}
}
