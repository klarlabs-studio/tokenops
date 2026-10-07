package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// POST /api/sources/refresh asks the pollers to poll now; one too soon
// after the last is refused with when to try again.
func TestSourcesRefreshAsksThePollers(t *testing.T) {
	next := time.Date(2026, 10, 7, 12, 0, 30, 0, time.UTC)
	accepted := true
	s := New("127.0.0.1:0", WithSourcesRefresh(func(time.Time) (int, bool, time.Time) {
		if accepted {
			return 3, true, next
		}
		return 0, false, next
	}))
	mux := http.NewServeMux()
	s.registerSourcesRoute(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// post answers the status, the Retry-After header and the body.
	post := func() (int, string, map[string]any) {
		resp, err := http.Post(ts.URL+"/api/sources/refresh", "application/json", nil) //nolint:noctx // test
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, resp.Header.Get("Retry-After"), body
	}
	status, _, body := post()
	if status != http.StatusAccepted || body["requested"] != true || body["pollers"] != float64(3) {
		t.Errorf("accepted refresh: %d %v", status, body)
	}
	accepted = false
	status, retry, body := post()
	if status != http.StatusTooManyRequests || body["requested"] != false || body["next_at"] != next.Format(time.RFC3339) {
		t.Errorf("refused refresh: %d %v", status, body)
	}
	if retry == "" {
		t.Error("refused refresh without Retry-After")
	}
}
