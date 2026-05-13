package proxy

import (
	_ "embed"
	"net/http"
)

// The dashboard is shipped as a single self-contained HTML file so it
// reuses the daemon's existing analytics surface (/api/spend/*) and
// doesn't introduce a build step. Vue + D3 are loaded from a CDN at
// page load; if the operator runs the daemon air-gapped the JSON
// endpoints still respond — the dashboard just won't render charts.
//
// A future revision can swap to a bundled offline build under
// web/dashboard/ + go:embed.FS without changing the public route.

//go:embed dashboard.html
var dashboardHTML []byte

// DashboardEnabled reports whether the dashboard route is mounted on
// the proxy's HTTP listener. The MCP `tokenops_dashboard` tool reads
// this flag (via the daemon URL hint file) to decide whether to
// return a navigable URL or a structured error.
const DashboardEnabled = true

// registerDashboard mounts the dashboard at /dashboard and
// /dashboard/. Mounted unconditionally when analytics handlers are
// installed; the dashboard depends on /api/spend/* responding.
func registerDashboard(mux *http.ServeMux) {
	h := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(dashboardHTML)
	}
	mux.HandleFunc("GET /dashboard", h)
	mux.HandleFunc("GET /dashboard/", h)
}
