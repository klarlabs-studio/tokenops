package proxy

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/version"
)

var startedAt = time.Now()

// ready reports whether the daemon has finished initialisation. Future tasks
// (storage, providers, optimizer) flip this to true once their bootstrap is
// complete.
var ready atomic.Bool

// readyState carries the optional blockers + next_actions slices the
// daemon publishes alongside the boolean readiness signal. Protected by
// readyMu because slice values aren't safe under naked atomic store.
var (
	readyMu          sync.RWMutex
	readyBlockers    []string
	readyNextActions []string
)

// MarkReady signals to /readyz that the daemon's dependencies are healthy.
func MarkReady(b bool) { ready.Store(b) }

// SetReadyState records subsystem blockers and remediation hints so
// /readyz can surface them alongside the readiness flag. The slices are
// copied to insulate readers from later daemon mutation. Pass nil/empty
// slices once everything is healthy.
func SetReadyState(blockers, nextActions []string) {
	readyMu.Lock()
	readyBlockers = append([]string(nil), blockers...)
	readyNextActions = append([]string(nil), nextActions...)
	readyMu.Unlock()
}

func snapshotReadyState() (blockers, nextActions []string) {
	readyMu.RLock()
	defer readyMu.RUnlock()
	if readyBlockers == nil {
		blockers = []string{}
	} else {
		blockers = append([]string(nil), readyBlockers...)
	}
	if readyNextActions == nil {
		nextActions = []string{}
	} else {
		nextActions = append([]string(nil), readyNextActions...)
	}
	return blockers, nextActions
}

// IsReady returns the current readiness state. Used by the MCP
// tokenops_status tool to surface the same signal /readyz reports over
// HTTP, without requiring the daemon to call itself.
func IsReady() bool { return ready.Load() }

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
	blockers, nextActions := snapshotReadyState()
	status := "not_ready"
	code := http.StatusServiceUnavailable
	if ready.Load() {
		status = "ready"
		code = http.StatusOK
	} else if len(blockers) > 0 {
		status = "not_configured"
	}
	writeJSON(w, code, map[string]any{
		"status":       status,
		"blockers":     blockers,
		"next_actions": nextActions,
	})
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
