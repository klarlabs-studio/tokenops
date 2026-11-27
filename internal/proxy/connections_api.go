package proxy

import (
	"errors"
	"net/http"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/capability/connections"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/providersetup"
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
// suggested; GET and POST /api/connections/connect show and grant another
// application's sign-in (ADR 0013). nil leaves them out.
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
	mux.HandleFunc("GET /api/connections/connect", func(w http.ResponseWriter, r *http.Request) {
		c, err := providersetup.FindConsent(r.Context(), r.URL.Query().Get("provider"), s.actions().AppLogin.Env)
		if err != nil {
			writeAPIError(w, connectStatus(err), err)
			return
		}
		writeAPIJSON(w, http.StatusOK, c)
	})
	mux.HandleFunc("POST /api/connections/connect", s.action("connection_connect", restart, func(r *http.Request, path string) (any, error) {
		var in ConnectConnectionRequest
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		got, err := providersetup.ConnectAppLogin(r.Context(), path, in.Provider,
			providersetup.Shown{What: in.What, Host: in.Host}, s.actions().AppLogin)
		if err != nil {
			return nil, statusError{status: connectStatus(err), err: err}
		}
		s.staleGlance()
		return got, nil
	}))
}

// ConnectConnectionRequest grants provider's other-application sign-in on
// the operator's yes to exactly what GET /api/connections/connect showed:
// its what and host.
type ConnectConnectionRequest struct {
	Provider string `json:"provider"`
	What     string `json:"what"`
	Host     string `json:"host"`
}

// connectStatus is the HTTP status a failed connect answers with.
func connectStatus(err error) int {
	var ce *providersetup.ConnectError
	if !errors.As(err, &ce) {
		return http.StatusInternalServerError
	}
	switch ce.Failure {
	case providersetup.ConnectNoSignIn:
		return http.StatusNotFound
	case providersetup.ConnectChanged:
		return http.StatusConflict
	case providersetup.ConnectRefused:
		return http.StatusUnprocessableEntity
	case providersetup.ConnectKeychain:
		return http.StatusForbidden
	case providersetup.ConnectUnreadable:
		return http.StatusBadGateway
	}
	return http.StatusBadRequest
}

// staleGlance makes the next glance request compute a new one.
func (s *Server) staleGlance() {
	if s.glances != nil {
		s.glances.stale()
	}
}
