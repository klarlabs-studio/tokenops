package proxy

import (
	"net/http"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/spending"
)

// spendTags is the window's spend per value of an allocation key, from the
// capability `tokenops spend --by <key>` and the MCP view use:
//
//	GET /api/spend/tags?by=project|client|cost_center&since=&until=&include_sources=
//
// The rules come from the daemon's configuration, and are evaluated here,
// on the daemon. The answer is served locally only; no upload carries it.
func (s *Server) spendTags(w http.ResponseWriter, r *http.Request) {
	key := allocation.Project
	if by := r.URL.Query().Get("by"); by != "" {
		k, err := allocation.ParseKey(by)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		key = k
	}
	since := time.Now().AddDate(0, 0, -allocation.DefaultDays)
	var until time.Time
	if r.URL.Query().Get("since") != "" || r.URL.Query().Get("until") != "" {
		s0, u, err := sinceUntil(r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		if r.URL.Query().Get("since") != "" {
			since = s0
		}
		until = u
	}
	var rules []allocation.Rule
	if s.plans != nil {
		if cfg := s.plans().Config; cfg != nil {
			rules = cfg.AllocationRules()
		}
	}
	res, err := allocation.Compute(r.Context(), allocation.Deps{
		Turns: commits.TurnsIn(s.analytics.aggregator, spending.Window{IncludeSources: includeSources(r)}), Rules: rules,
	}, key, since, until)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, res)
}
