package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEventsAPISurfacesCounts(t *testing.T) {
	mux := http.NewServeMux()
	srv := &Server{eventCounts: func() map[string]int64 {
		return map[string]int64{
			"workflow.started":     3,
			"optimization.applied": 1,
		}
	}}
	srv.registerEventCountsRoute(mux)

	ts := httptest.NewServer(mux)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/domain-events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var got struct {
		Counts map[string]int64 `json:"counts"`
		Total  int64            `json:"total"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if got.Total != 4 {
		t.Errorf("total = %d, want 4", got.Total)
	}
	if got.Counts["workflow.started"] != 3 {
		t.Errorf("workflow.started = %d", got.Counts["workflow.started"])
	}
}

func TestEventsAPISkipsWhenNotConfigured(t *testing.T) {
	mux := http.NewServeMux()
	srv := &Server{}
	srv.registerEventCountsRoute(mux)

	ts := httptest.NewServer(mux)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/domain-events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404 when no counter wired", resp.StatusCode)
	}
}

func TestEventsAPIServesWhenEachKindHappened(t *testing.T) {
	june := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	srv := &Server{
		eventCounts: func() map[string]int64 { return map[string]int64{"budget.exceeded": 274} },
		eventSpans:  func() map[string]EventSpan { return map[string]EventSpan{"budget.exceeded": {First: june, Last: june}} },
	}
	srv.registerEventCountsRoute(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/domain-events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got struct {
		Spans map[string]EventSpan `json:"spans"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Spans["budget.exceeded"].Last.Equal(june) {
		t.Errorf("spans = %+v, want last_at in June", got.Spans)
	}
}
