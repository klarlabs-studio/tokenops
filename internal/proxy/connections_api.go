package proxy

import (
	"net/http"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/capability/connections"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/state"
)

// ConnectionsFunc computes the connections a glance carries, from its
// figures (the turns it read), the readers' health and the time.
type ConnectionsFunc func(g headroom.Glance, health []state.SourceReport, now time.Time) []connections.Connection

// WithConnections has each glance carry its connections: per provider the
// operator uses or has installed, whether its plan is read and, where it
// is not, the one thing to do. They are computed with the glance, on its
// cadence, never per request, and served on GET /api/glance and
// GET /api/connections; POST /api/connections/dismiss stops one being
// suggested. nil leaves them out.
func WithConnections(fn ConnectionsFunc) Option {
	return func(s *Server) { s.connections = fn }
}

// ConnectionsReport is GET /api/connections: the glance's connections and
// when they were computed.
type ConnectionsReport struct {
	Connections []connections.Connection `json:"connections"`
	ComputedAt  time.Time                `json:"computed_at,omitzero"`
}

// DismissConnectionRequest stops TokenOps suggesting to connect provider,
// or with undo, suggests it again.
type DismissConnectionRequest struct {
	Provider string `json:"provider"`
	Undo     bool   `json:"undo,omitempty"`
}

func (s *Server) registerConnectionRoutes(mux RouteMux) {
	if s.connections == nil || s.plans == nil {
		return
	}
	mux.HandleFunc("GET /api/connections", func(w http.ResponseWriter, r *http.Request) {
		v, _, err := s.view(r.Context(), time.Now().UTC())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		out := ConnectionsReport{Connections: []connections.Connection{}}
		if v.Glance != nil {
			out.ComputedAt = v.Glance.ComputedAt
			if v.Glance.Connections != nil {
				out.Connections = v.Glance.Connections
			}
		}
		writeAPIJSON(w, http.StatusOK, out)
	})
	if s.actions == nil || s.dashAuth == nil {
		return
	}
	mux.HandleFunc("POST /api/connections/dismiss", s.action("connection_dismiss", noRestart, func(r *http.Request, path string) (any, error) {
		var in DismissConnectionRequest
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		change, err := actions.DismissConnection(path, in.Provider, in.Undo)
		if err == nil {
			// The next glance computes them again with the dismissal.
			s.staleGlance()
		}
		return change, err
	}))
}

// staleGlance makes the next glance request compute a new one.
func (s *Server) staleGlance() {
	if s.glances != nil {
		s.glances.stale()
	}
}
