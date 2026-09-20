package proxy

import (
	"net/http"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
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
func WithSourceFreshness(fn func() []freshness.Report) Option {
	return func(s *Server) { s.sourceFreshness = fn }
}

// sourcesResponse is the wire shape of GET /api/sources.
type sourcesResponse struct {
	// Sources is every configured source, healthy ones included. A
	// caller that wants only the problems can filter; a caller that
	// wants to show an operator what is being watched could not
	// previously do so at all.
	Sources []freshness.Report `json:"sources"`
	// Unhealthy is how many need attention, so a caller can decide
	// whether to render anything without walking the list.
	Unhealthy int `json:"unhealthy"`
}

func (s *Server) registerSourcesRoute(mux *http.ServeMux) {
	if s.sourceFreshness == nil {
		return
	}
	mux.HandleFunc("GET /api/sources", func(w http.ResponseWriter, _ *http.Request) {
		reports := s.sourceFreshness()
		unhealthy := 0
		for _, r := range reports {
			if !r.Healthy() {
				unhealthy++
			}
		}
		if reports == nil {
			// Render an empty array rather than null: a caller iterating
			// the field should not have to special-case the difference.
			reports = []freshness.Report{}
		}
		writeAPIJSON(w, http.StatusOK, sourcesResponse{
			Sources:   reports,
			Unhealthy: unhealthy,
		})
	})
}
