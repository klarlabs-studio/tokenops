package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

var octoberEighth = func() time.Time { return time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC) }

// The fixture follows the example in OpenAI's Costs API reference, the
// answer CodexBar's openai.js parses: daily buckets of results, each an
// amount in USD.
func TestOpenAIAdminSumsTheMonth(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-admin-x" || r.URL.Path != "/v1/organization/costs" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(fixture(t, "openai")))
	}))
	defer srv.Close()
	got, err := OpenAIAdmin{BaseURL: srv.URL, Now: octoberEighth}.Read(context.Background(), "sk-admin-x")
	if err != nil || !got.HasUsed || !approx(got.UsedUSD, 15) || got.LimitUSD != 0 || got.Subscription || got.Scope != "organization" {
		t.Fatalf("got %+v, %v", got, err)
	}
	// From the first of the month to the end of today, in daily buckets.
	if want := "bucket_width=1d&end_time=1791504000&limit=31&start_time=1790812800"; query != want {
		t.Errorf("query %s, want %s", query, want)
	}
}

func TestOpenAIAdminFollowsPages(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("page") == "" {
			_, _ = w.Write([]byte(`{"data":[{"results":[{"amount":{"value":1,"currency":"usd"}}]}],"has_more":true,"next_page":"p2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"results":[{"amount":{"value":"2.5","currency":"usd"}},{"amount":null}]}],"has_more":false}`))
	}))
	defer srv.Close()
	got, err := OpenAIAdmin{BaseURL: srv.URL, Now: octoberEighth}.Read(context.Background(), "k")
	if err != nil || calls != 2 || !approx(got.UsedUSD, 3.5) {
		t.Fatalf("got %+v, %v after %d calls", got, err, calls)
	}
}

func TestOpenAIAdminRefusalsAndShapes(t *testing.T) {
	srv := serve(t, "/v1/organization/costs", "sk-admin-x", `{"data":[{"results":[{"amount":{"value":1,"currency":"eur"}}]}],"has_more":false}`)
	defer srv.Close()
	// A project or service-account key cannot read organisation costs.
	if _, err := (OpenAIAdmin{BaseURL: srv.URL}).Read(context.Background(), "sk-proj-x"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("refused key: %v", err)
	}
	if _, err := (OpenAIAdmin{BaseURL: srv.URL}).Read(context.Background(), "sk-admin-x"); err == nil {
		t.Error("a cost in another currency was summed as dollars")
	}
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[],"has_more":true,"next_page":"same"}`))
	}))
	defer loop.Close()
	if _, err := (OpenAIAdmin{BaseURL: loop.URL}).Read(context.Background(), "k"); err == nil {
		t.Error("a repeated cursor was followed")
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"page","data":[],"has_more":false}`))
	}))
	defer empty.Close()
	// No spend yet this month is a reading of $0, not nothing.
	if got, err := (OpenAIAdmin{BaseURL: empty.URL}).Read(context.Background(), "k"); err != nil || !got.HasUsed || got.UsedUSD != 0 {
		t.Errorf("empty month: %+v, %v", got, err)
	}
}
