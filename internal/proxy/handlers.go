package proxy

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/version"
)

var startedAt = time.Now()

// ready reports whether the daemon has finished initialisation. Future tasks
// (storage, providers, optimizer) flip this to true once their bootstrap is
// complete.
var ready atomic.Bool

// MarkReady signals to /readyz that the daemon's dependencies are healthy.
func MarkReady(b bool) { ready.Store(b) }

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", healthzHandler)
	mux.HandleFunc("GET /readyz", readyzHandler)
	mux.HandleFunc("GET /version", versionHandler)
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"started_at": startedAt.UTC(),
		"uptime_ns":  time.Since(startedAt).Nanoseconds(),
	})
}

func readyzHandler(w http.ResponseWriter, _ *http.Request) {
	if ready.Load() {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
}

func versionHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version": version.Version,
		"commit":  version.Commit,
		"date":    version.Date,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
