package proxy

import (
	"net/http"
)

// subsystemDisabledHandler returns an HTTP handler that responds with
// 503 Service Unavailable and a structured `{error, hint}` body. Used to
// shadow routes for subsystems the daemon was started without (storage
// off → analytics+audit routes; rules off → /api/rules/*). Operators on
// a fresh install see actionable guidance instead of an opaque 404.
func subsystemDisabledHandler(reason, hint string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": reason,
			"hint":  hint,
		})
	}
}

// storageDisabledPaths is the canonical list of analytics + audit routes
// that require the sqlite event store. Kept in one place so the
// disabled-handler registration stays in sync with the real handlers in
// api.go and audit_api.go.
var storageDisabledPaths = []string{
	"GET /api/spend/summary",
	"GET /api/spend/series",
	"GET /api/spend/forecast",
	"GET /api/workflows",
	"GET /api/workflows/{id}",
	"GET /api/optimizations",
	"GET /api/audit",
}

// rulesDisabledPaths matches the rule intelligence routes registered by
// RulesHandlers.Register. Kept in sync manually because Go's net/http
// ServeMux has no introspection.
var rulesDisabledPaths = []string{
	"GET /api/rules/analyze",
	"GET /api/rules/conflicts",
	"GET /api/rules/compress",
	"GET /api/rules/inject",
}
