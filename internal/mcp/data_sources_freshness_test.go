package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

// freshnessDaemon stands in for the ingestion daemon's /api/sources.
func freshnessDaemon(t *testing.T, reports []freshness.Report) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sources" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		unhealthy := 0
		for _, rep := range reports {
			if !rep.Healthy() {
				unhealthy++
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sources": reports, "unhealthy": unhealthy,
		})
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

// tokenops_data_sources answered with counts and nothing else. An agent
// could see that a source produced 0 events and had no way to learn
// whether that meant "you stopped using Cursor" or "the poller has been
// refused for three days" — opposite situations with opposite remedies.
func TestDataSourcesReportsFreshnessFromTheDaemon(t *testing.T) {
	at := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	url := freshnessDaemon(t, []freshness.Report{{
		Name: "Cursor", Tag: "cursor-web",
		State: freshness.StateFailing, Severity: freshness.SeverityDegraded,
		LastEventAt: at, SilentFor: 3 * time.Hour,
		LastSuccessfulPollAt: at, LastError: "401 unauthorized",
	}})

	got, err := FetchDaemonSources(url)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got.Sources) != 1 {
		t.Fatalf("want 1 source, got %+v", got.Sources)
	}
	s := got.Sources[0]
	if s.State != freshness.StateFailing {
		t.Errorf("state = %q", s.State)
	}
	if s.LastError != "401 unauthorized" {
		t.Errorf("the error did not survive the fetch: %q", s.LastError)
	}
	if !s.LastEventAt.Equal(at) {
		t.Errorf("last-seen = %v, want %v", s.LastEventAt, at)
	}
	if got.Unhealthy != 1 {
		t.Errorf("unhealthy = %d", got.Unhealthy)
	}
}

// The tool works without a reachable daemon. Counts are read from the
// store this process already has; freshness needs the daemon, and its
// absence must degrade the answer rather than fail it.
func TestDataSourcesStillAnswersWithoutADaemon(t *testing.T) {
	var d DataSourcesDeps
	d.Sources = func() (DaemonSources, error) {
		return DaemonSources{}, errDaemonUnreachable
	}

	res := d.freshness(context.Background())
	if res != nil {
		t.Errorf("an unreachable daemon produced %+v, want nothing", res)
	}
}

// When the daemon does answer, the tool passes the assessment through
// rather than re-deriving it: the daemon is the only process that knows
// all three facts, and two surfaces computing "healthy" separately is
// how they come to disagree.
func TestDataSourcesPassesTheDaemonsAssessmentThrough(t *testing.T) {
	var d DataSourcesDeps
	d.Sources = func() (DaemonSources, error) {
		return DaemonSources{Sources: []freshness.Report{
			{Tag: "cursor-web", State: freshness.StateIdle, Severity: freshness.SeverityOK},
		}}, nil
	}

	res := d.freshness(context.Background())
	if len(res) != 1 || res[0].State != freshness.StateIdle {
		t.Errorf("got %+v", res)
	}
}
