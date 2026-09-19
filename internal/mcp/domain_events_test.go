package mcp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Domain events are counted inside the ingestion daemon. The MCP server is a
// different process and never had the counters, so the tool used to answer
// {"counts":{},"total":0} — "nothing happened" — for a fact it could not see.
// With no way to reach the daemon it must say so instead.
func TestDomainEventsUnavailableWithoutDaemonIsNotZero(t *testing.T) {
	srv := newControlServer(t, ControlDeps{})
	out := execTool(t, srv, "tokenops_domain_events", nil)
	if !strings.Contains(out, `"error": "unavailable_in_mcp_server"`) {
		t.Errorf("want the unavailable marker: %s", out)
	}
	if !strings.Contains(out, `"hint"`) {
		t.Errorf("want a hint naming where the counts live: %s", out)
	}
	for _, zero := range []string{`"total"`, `"counts"`} {
		if strings.Contains(out, zero) {
			t.Errorf("unavailable counts must not be rendered as %s: %s", zero, out)
		}
	}
}

func TestDomainEventsUnavailableWhenDaemonUnreachable(t *testing.T) {
	res := domainEventsInfo(ControlDeps{
		DaemonProbe: func() DaemonReport { return DaemonReport{} },
		DaemonDomainEvents: func(string) (DaemonDomainEvents, error) {
			t.Fatal("fetched counts from an unreachable daemon")
			return DaemonDomainEvents{}, nil
		},
	})
	if res.Error != "unavailable_in_mcp_server" || res.Total != nil || res.Counts != nil {
		t.Errorf("unreachable daemon rendered as data: %+v", res)
	}
	if !strings.Contains(res.Hint, "tokenops daemon restart") || strings.Contains(res.Hint, "'tokenops start'") {
		t.Errorf("hint should say how to get a daemon, without starting a duplicate: %q", res.Hint)
	}
}

func TestDomainEventsUnavailableWhenDaemonFetchFails(t *testing.T) {
	res := domainEventsInfo(ControlDeps{
		DaemonProbe: func() DaemonReport { return DaemonReport{URL: "http://127.0.0.1:9", Alive: true} },
		DaemonDomainEvents: func(string) (DaemonDomainEvents, error) {
			return DaemonDomainEvents{}, errors.New("boom")
		},
	})
	if res.Error != "unavailable_in_mcp_server" || res.Total != nil {
		t.Errorf("failed fetch rendered as data: %+v", res)
	}
}

func TestDomainEventsFromDaemon(t *testing.T) {
	dropped := int64(2)
	var asked string
	res := domainEventsInfo(ControlDeps{
		DaemonProbe: func() DaemonReport { return DaemonReport{URL: "http://127.0.0.1:9", Alive: true} },
		DaemonDomainEvents: func(url string) (DaemonDomainEvents, error) {
			asked = url
			return DaemonDomainEvents{
				Counts:       map[string]int64{"workflow.started": 3, "budget.exceeded": 1},
				Total:        4,
				AuditDropped: &dropped,
			}, nil
		},
	})
	if asked != "http://127.0.0.1:9" {
		t.Errorf("fetched from %q, want the probed daemon", asked)
	}
	if res.Error != "" {
		t.Fatalf("unexpected error: %+v", res)
	}
	if res.Total == nil || *res.Total != 4 || res.Counts["workflow.started"] != 3 {
		t.Errorf("counts not relayed: %+v", res)
	}
	if res.AuditDropped == nil || *res.AuditDropped != 2 {
		t.Errorf("audit drops not relayed: %+v", res)
	}
	if res.Source != "daemon" {
		t.Errorf("source = %q, want daemon", res.Source)
	}
}

func TestFetchDaemonDomainEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/domain-events" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"counts":        map[string]int64{"optimization.applied": 5},
			"total":         5,
			"audit_dropped": 0,
		})
	}))
	defer srv.Close()

	got, err := FetchDaemonDomainEvents(srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Total != 5 || got.Counts["optimization.applied"] != 5 {
		t.Errorf("decoded %+v", got)
	}
	if got.AuditDropped == nil || *got.AuditDropped != 0 {
		t.Errorf("audit_dropped present in the payload must survive decoding: %+v", got)
	}
}

// A daemon too old to serve the route answers 404; that is "unknown", not an
// empty set of counts — even when the error body happens to decode as one.
func TestFetchDaemonDomainEventsRejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"counts":{},"total":0}`))
	}))
	defer srv.Close()
	if _, err := FetchDaemonDomainEvents(srv.URL); err == nil {
		t.Fatal("404 decoded as counts")
	}
}
