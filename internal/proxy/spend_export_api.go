package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
)

// spendExport serves the window's spend and usage as a FOCUS dataset, the
// FinOps billing format, from the capability `tokenops spend export` uses:
//
//	GET /api/spend/export?format=focus&since=&until=&include_sources=
//
// The plans in force come from the plan routes' configuration, when they
// are mounted, and the plan history. Like every route it serves derived
// figures only.
func (s *Server) spendExport(w http.ResponseWriter, r *http.Request) {
	if f := r.URL.Query().Get("format"); f != "" && !strings.EqualFold(f, "focus") {
		writeAPIError(w, http.StatusBadRequest, fmt.Errorf("format %q is not supported; the export format is focus", f))
		return
	}
	q := spending.FocusQuery{IncludeSources: includeSources(r), Now: time.Now()}
	if r.URL.Query().Get("since") != "" || r.URL.Query().Get("until") != "" {
		since, until, err := sinceUntil(r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		if r.URL.Query().Get("since") != "" {
			q.Since = since
		}
		q.Until = until
	}
	if s.plans != nil {
		if cfg := s.plans().Config; cfg != nil {
			q.Plans = cfg.Plans
			q.Allocation = &allocation.Deps{Rules: cfg.AllocationRules()}
		}
	}
	if file, err := planhistory.Default(); err == nil {
		h, err := file.Load()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, fmt.Errorf("read plan history: %w", err))
			return
		}
		q.History = h
	}
	d, err := spending.FocusExport(r.Context(), s.analytics.aggregator, q)
	if errors.Is(err, spending.ErrEmptyWindow) {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	var buf bytes.Buffer
	if err := d.WriteCSV(&buf); err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="tokenops-focus-%s-%s.csv"`,
		d.Since.Format("20060102"), d.Until.Format("20060102")))
	w.Header().Set("X-Focus-Version", d.Version)
	_, _ = w.Write(buf.Bytes())
}
