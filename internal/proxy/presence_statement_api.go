package proxy

import (
	"errors"
	"net/http"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/capability/boundaries"
)

// Statement routes hand a surface the exact statement an approval signs
// (ADR 0015 §3), so the menu bar can approve inline: it asks for the
// statement, has the presence helper sign it (the dialog shows Reason),
// and posts the proof to the matching decisions route.
//
//	POST /api/boundaries/statements {"id", "session"}
//	POST /api/routing/statements    {"key"}
//
// The statement is built only by the daemon, never by the surface, so the
// bytes the person approves are the bytes the verifier checks. Handing one
// out grants nothing: it is worthless without a signature from the
// enrolled key, which only the dialog can give. They are mounted only
// behind the API token, like the action routes, and are not audited: they
// change nothing (a decision raised before challenges existed is given
// one).

// BoundaryStatementRequest asks for the statement approving a boundary decision that waits, for the next step or the session.
type BoundaryStatementRequest struct {
	ID      string `json:"id"`
	Session bool   `json:"session"`
}

// RoutingStatementRequest asks for the statement approving a pending routing upgrade.
type RoutingStatementRequest struct {
	Key string `json:"key"`
}

// presenceUnavailable is the error code of a statement that cannot be
// signed on this machine (no key enrolled, a held key, no blob): a
// surface falls back to the terminal.
const presenceUnavailable = "presence_unavailable"

func (s *Server) registerStatementRoutes(mux RouteMux) {
	if s.actions == nil || s.dashAuth == nil {
		return
	}
	mux.HandleFunc("POST /api/boundaries/statements", statement(func(r *http.Request) (any, error) {
		var in BoundaryStatementRequest
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		return boundaries.PrepareApproval(boundaries.Env{}, in.ID, in.Session)
	}))
	mux.HandleFunc("POST /api/routing/statements", statement(func(r *http.Request) (any, error) {
		var in RoutingStatementRequest
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		return actions.PrepareRoutingApproval("", in.Key)
	}))
}

func statement(do func(r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := do(r)
		switch {
		case err == nil:
			writeAPIJSON(w, http.StatusOK, out)
		case errors.Is(err, boundaries.ErrPresenceUnavailable) || errors.Is(err, actions.ErrRoutingPresenceUnavailable):
			writeAPIJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": presenceUnavailable})
		case actions.IsInput(err):
			writeAPIError(w, http.StatusBadRequest, err)
		default:
			writeAPIError(w, http.StatusInternalServerError, err)
		}
	}
}
