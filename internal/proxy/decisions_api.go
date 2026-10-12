package proxy

import (
	"errors"
	"net/http"

	"go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/decisions"
	"go.klarlabs.de/tokenops/internal/config"
)

// Decision routes answer from the same capability the MCP tools call
// (ADR 0010, slice 3):
//
//	GET /api/routing/proposals   upgrades waiting on the operator
//	GET /api/decisions/{id}      why a decision was made
//	GET /api/boundaries          each boundary rule and what waits on the operator

// proposalsHandler serves pending routing proposals. They live in their own
// file, not the event store, so the route is mounted whatever else is.
func proposalsHandler(w http.ResponseWriter, _ *http.Request) {
	res, err := decisions.PendingProposals("")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, res)
}

func (a *AnalyticsHandlers) decision(w http.ResponseWriter, r *http.Request) {
	res, err := decisions.Explain(r.Context(), a.store, r.PathValue("id"))
	switch {
	case errors.Is(err, decisions.ErrMissingID):
		writeAPIError(w, http.StatusBadRequest, err)
	case err != nil:
		writeAPIError(w, http.StatusInternalServerError, err)
	case res.Error == "decision_not_found":
		writeAPIJSON(w, http.StatusNotFound, res)
	default:
		writeAPIJSON(w, http.StatusOK, res)
	}
}

// boundariesHandler serves each boundary rule's stage, matches and
// enforcement, and the decisions waiting on the operator (ADR 0014). The
// rules are read from the config the daemon was started with.
func (s *Server) boundariesHandler(w http.ResponseWriter, _ *http.Request) {
	path := ""
	if s.actions != nil {
		path = s.actions().ConfigPath
	}
	if path == "" {
		p, err := config.DefaultPath()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		path = p
	}
	cfg, err := config.Load(path)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	st, err := boundaries.RuleStatuses(boundaries.AllRules(cfg), boundaries.Env{}, boundaries.Wired(cfg))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, st)
}
