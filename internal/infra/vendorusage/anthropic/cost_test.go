package anthropic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// cost_report states cents as decimal strings, across pages; the client
// returns dollars per UTC day, and a day without costs as 0.
func TestDailyCostReadsCentsAcrossPages(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/organizations/cost_report" || r.Header.Get("x-api-key") != "sk-ant-admin01-x" ||
			r.URL.Query().Get("bucket_width") != "1d" {
			t.Errorf("request %s %v", r.URL, r.Header)
		}
		if r.URL.Query().Get("page") == "" {
			_, _ = w.Write([]byte(`{"data":[{"starting_at":"2026-10-07T00:00:00Z","ending_at":"2026-10-08T00:00:00Z",
				"results":[{"amount":"1234.5","currency":"USD"},{"amount":"100","currency":"USD"}]}],
				"has_more":true,"next_page":"p2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"starting_at":"2026-10-08T00:00:00Z","ending_at":"2026-10-09T00:00:00Z","results":[]}],
			"has_more":false,"next_page":null}`))
	}))
	defer srv.Close()
	c := NewAdminClient("sk-ant-admin01-x")
	c.BaseURL = srv.URL
	got, err := c.DailyCost(context.Background(), time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || got["2026-10-07"] != 13.345 || got["2026-10-08"] != 0 || len(got) != 2 {
		t.Errorf("calls %d, days %v", calls, got)
	}
	if _, err := NewAdminClient("").DailyCost(context.Background(), time.Now(), time.Now()); err == nil {
		t.Error("no key, no error")
	}
}
