package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

func freshnessServer(t *testing.T, reports []freshness.Report) *httptest.Server {
	t.Helper()
	s := New("127.0.0.1:0", WithSourceFreshness(func() []freshness.Report { return reports }))
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	protected := http.NewServeMux()
	s.registerSourcesRoute(protected)
	mux.Handle("/api/", protected)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// Freshness has only ever existed as a warning sentence assembled inside
// `tokenops status`. Nothing could read it as data — not the dashboard,
// not an agent, not a script — so "which of my sources is still working"
// had no answer any program could act on.
func TestSourcesEndpointReportsFreshnessAsData(t *testing.T) {
	at := time.Date(2026, 9, 20, 11, 0, 0, 0, time.UTC)
	ts := freshnessServer(t, []freshness.Report{
		{
			Name: "Cursor", Tag: "cursor-web",
			State: freshness.StateFailing, Severity: freshness.SeverityDegraded,
			LastEventAt: at, SilentFor: 3 * time.Hour,
			LastPollAt: at.Add(3 * time.Hour), LastSuccessfulPollAt: at,
			LastError: "401 unauthorized",
		},
	})

	resp, err := http.Get(ts.URL + "/api/sources") //nolint:noctx // test
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var got struct {
		Sources []struct {
			Tag                  string `json:"tag"`
			State                string `json:"state"`
			Severity             string `json:"severity"`
			LastEventAt          string `json:"last_event_at"`
			LastSuccessfulPollAt string `json:"last_successful_poll_at"`
			LastError            string `json:"last_error"`
		} `json:"sources"`
		Unhealthy int `json:"unhealthy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("want 1 source, got %+v", got.Sources)
	}
	s := got.Sources[0]
	if s.Tag != "cursor-web" || s.State != "failing" {
		t.Errorf("source = %+v", s)
	}
	if s.LastEventAt == "" || s.LastSuccessfulPollAt == "" {
		t.Errorf("timestamps were not rendered: %+v", s)
	}
	if s.LastError != "401 unauthorized" {
		t.Errorf("the error did not reach the caller: %q", s.LastError)
	}
	if got.Unhealthy != 1 {
		t.Errorf("unhealthy = %d, want 1", got.Unhealthy)
	}
}

// A healthy fleet answers with sources and a zero count, not an empty
// body. "Everything is fine" and "nothing is configured" must not look
// the same, which is exactly what the warning-only check did.
func TestSourcesEndpointListsHealthySourcesToo(t *testing.T) {
	ts := freshnessServer(t, []freshness.Report{
		{Name: "Cursor", Tag: "cursor-web", State: freshness.StateHealthy, Severity: freshness.SeverityOK},
	})

	resp, err := http.Get(ts.URL + "/api/sources") //nolint:noctx // test
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var got struct {
		Sources   []map[string]any `json:"sources"`
		Unhealthy int              `json:"unhealthy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Sources) != 1 {
		t.Errorf("a healthy source was omitted: %+v", got)
	}
	if got.Unhealthy != 0 {
		t.Errorf("unhealthy = %d, want 0", got.Unhealthy)
	}
}

// Without the option the route is not mounted at all, rather than
// answering with an empty list that would read as "no sources".
func TestSourcesRouteIsAbsentWithoutTheOption(t *testing.T) {
	s := New("127.0.0.1:0")
	mux := http.NewServeMux()
	protected := http.NewServeMux()
	s.registerSourcesRoute(protected)
	mux.Handle("/api/", protected)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/sources") //nolint:noctx // test
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Error("the route answered without a freshness source wired")
	}
}
