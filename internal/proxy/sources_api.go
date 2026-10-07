package proxy

import (
	"net/http"

	"go.klarlabs.de/tokenops/internal/capability/state"
)

// WithSourceFreshness exposes per-source ingestion health on
// GET /api/sources.
//
// Freshness has only ever existed as a warning sentence assembled inside
// `tokenops status`: prose, produced on demand, reaching one caller. No
// program could read it, so "which of my sources is still working" had
// no answer a dashboard, an agent or a script could act on — and the
// daemon that knows the answer is a different process from the MCP
// server that gets asked.
//
// Passing nil, or omitting the option, leaves the route unmounted. That
// is deliberate: an empty list would read as "no sources configured",
// which is a claim, where a missing route is plainly an absence.
func WithSourceFreshness(fn func() []state.SourceReport) Option {
	return func(s *Server) { s.sourceFreshness = fn }
}

func (s *Server) registerSourcesRoute(mux RouteMux) {
	if s.sourceFreshness == nil {
		return
	}
	mux.HandleFunc("GET /api/sources", func(w http.ResponseWriter, _ *http.Request) {
		writeAPIJSON(w, http.StatusOK, state.SourceHealthOf(s.sourceFreshness()))
	})
}
