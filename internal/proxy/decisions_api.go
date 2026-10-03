package proxy

import (
	"errors"
	"net/http"

	"go.klarlabs.de/tokenops/internal/capability/decisions"
)

// Decision routes answer from the same capability the MCP tools call
// (ADR 0010, slice 3):
//
//	GET /api/routing/proposals   upgrades waiting on the operator
//	GET /api/decisions/{id}      why a decision was made

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
