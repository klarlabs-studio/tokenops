package proxy

import (
	"math"
	"net/http"
	"strconv"
	"time"

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

// WithSourcesRefresh mounts POST /api/sources/refresh, which asks the
// usage pollers to poll now rather than at their next interval. A plan
// window polled every 15 minutes can be that old, and re-reading the
// daemon only returns the same reading again.
func WithSourcesRefresh(fn func(time.Time) (int, bool, time.Time)) Option {
	return func(s *Server) { s.sourcesRefresh = fn }
}

// SourcesRefreshResult is the answer to POST /api/sources/refresh.
type SourcesRefreshResult struct {
	// Requested says whether the pollers were asked; a refresh too soon
	// after the last is refused.
	Requested bool `json:"requested"`
	// Pollers is how many were asked to poll now.
	Pollers int `json:"pollers"`
	// NextAt is when the next refresh will be accepted.
	NextAt string `json:"next_at,omitempty"`
}

func (s *Server) registerSourcesRoute(mux RouteMux) {
	if s.sourcesRefresh != nil {
		mux.HandleFunc("POST /api/sources/refresh", func(w http.ResponseWriter, _ *http.Request) {
			now := time.Now()
			n, ok, next := s.sourcesRefresh(now)
			out := SourcesRefreshResult{Requested: ok, Pollers: n}
			if !next.IsZero() {
				out.NextAt = next.UTC().Format(time.RFC3339)
			}
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(next.Sub(now).Seconds())))))
				writeAPIJSON(w, http.StatusTooManyRequests, out)
				return
			}
			writeAPIJSON(w, http.StatusAccepted, out)
		})
	}
	if s.sourceFreshness == nil {
		return
	}
	mux.HandleFunc("GET /api/sources", func(w http.ResponseWriter, _ *http.Request) {
		writeAPIJSON(w, http.StatusOK, state.SourceHealthOf(s.sourceFreshness()))
	})
}
