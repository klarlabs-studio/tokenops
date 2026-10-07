package proxy

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/spending"
)

// Spending routes answer from the same capability the MCP tools call
// (ADR 0010, slice 3):
//
//	GET /api/spend/top?by=&top=&since=&until=&include_sources=
//	GET /api/spend/burn-rate?hours=&include_sources=
//	GET /api/scorecard?since_days=
//	GET /api/pricing?provider=&model=&limit=

func (a *AnalyticsHandlers) spendTop(w http.ResponseWriter, r *http.Request) {
	q := spending.TopQuery{By: r.URL.Query().Get("by"), IncludeSources: includeSources(r)}
	var err error
	if q.Top, err = intParam(r, "top"); err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if r.URL.Query().Get("since") != "" || r.URL.Query().Get("until") != "" {
		if q.Since, q.Until, err = sinceUntil(r); err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
	}
	res, err := spending.Top(r.Context(), a.aggregator, q, a.spend.Currency(), time.Now())
	if errors.Is(err, spending.ErrUnknownGrouping) {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, res)
}

func (a *AnalyticsHandlers) spendBurnRate(w http.ResponseWriter, r *http.Request) {
	hours, err := intParam(r, "hours")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	res, err := spending.BurnRate(r.Context(), a.aggregator, hours, includeSources(r), a.spend.Currency(), time.Now())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, res)
}

func (a *AnalyticsHandlers) scorecard(w http.ResponseWriter, r *http.Request) {
	days, err := intParam(r, "since_days")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, spending.Scorecard(r.Context(), a.store, spending.ScorecardParams{SinceDays: days}))
}

// pricingHandler serves the rate card. It needs no event store, so it is
// mounted whatever else is.
func pricingHandler(w http.ResponseWriter, r *http.Request) {
	limit, err := intParam(r, "limit")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	q := r.URL.Query()
	writeAPIJSON(w, http.StatusOK, spending.RateCard("", spending.RateQuery{Provider: q.Get("provider"), Model: q.Get("model"), Limit: limit}))
}

// includeSources reads ?include_sources=a,b (or repeated).
func includeSources(r *http.Request) []string {
	values := r.URL.Query()["include_sources"]
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strings.Split(v, ",")...)
	}
	return out
}

// spendCommits is what each of the operator's commits in a window cost:
// the agent work that led to it, priced. Commit subjects are the
// repository's own text, so the API withholds them.
func (a *AnalyticsHandlers) spendCommits(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-7 * 24 * time.Hour)
	if r.URL.Query().Get("since") != "" {
		s, _, err := sinceUntil(r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		since = s
	}
	res, err := commits.Compute(r.Context(), commits.Deps{Turns: commits.TurnsFrom(a.aggregator)}, since)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	limit, err := intParam(r, "limit")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if limit <= 0 {
		limit = 50
	}
	writeAPIJSON(w, http.StatusOK, res.Newest(limit).WithoutSubjects())
}
