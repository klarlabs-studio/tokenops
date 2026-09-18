package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func healthBody(t *testing.T, s *Server) map[string]any {
	t.Helper()
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode healthz: %v", err)
	}
	return body
}

func TestHealthzCarriesTheDropCount(t *testing.T) {
	s := &Server{eventDrops: func() int64 { return 30254 }}
	body := healthBody(t, s)
	got, ok := body["dropped_events"].(float64)
	if !ok {
		t.Fatalf("dropped_events missing or not a number: %#v", body)
	}
	if int64(got) != 30254 {
		t.Errorf("dropped_events = %v, want 30254", got)
	}
	// Losing rows is a downstream problem. A probe that turned 503 over it
	// would restart a process whose fault it is not.
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok — dropped rows must not fail liveness", body["status"])
	}
}

// Without the accessor wired (storage disabled, so there is no bus) the key
// is absent rather than zero: nothing counted is not the same fact as
// nothing lost.
func TestHealthzOmitsDropCountWhenUnwired(t *testing.T) {
	body := healthBody(t, &Server{})
	if _, present := body["dropped_events"]; present {
		t.Fatalf("dropped_events should be absent when no accessor is wired: %#v", body)
	}
}
