package proxy

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/state"
)

// WithState serves the control plane's own state from the same capability
// the MCP tools call (ADR 0010, slice 2):
//
//	GET /api/status         readiness, blockers, warnings, next actions
//	GET /api/mode           operating mode and what each subsystem may do
//	GET /api/coach          the coach's dials and each power's autonomy
//	GET /api/config         the active configuration, secrets redacted
//	GET /api/data-sources   events per source and each reader's health
//	GET /api/vendor-usage   which usage sources are on and producing
//
// deps is called per request. nil leaves the routes unmounted.
func WithState(deps func() state.Deps) Option {
	return func(s *Server) { s.state = deps }
}

func (s *Server) registerStateRoutes(mux RouteMux) {
	if s.state == nil {
		return
	}
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeAPIJSON(w, http.StatusOK, state.StatusOf(r.Context(), s.state(), time.Now()))
	})
	mux.HandleFunc("GET /api/mode", func(w http.ResponseWriter, _ *http.Request) {
		d := s.state()
		if d.Config == nil {
			writeAPIJSON(w, http.StatusOK, map[string]string{"error": "config_unavailable"})
			return
		}
		writeAPIJSON(w, http.StatusOK, state.ModeOf(*d.Config))
	})
	mux.HandleFunc("GET /api/coach", func(w http.ResponseWriter, _ *http.Request) {
		d := s.state()
		if d.Coach == nil {
			writeAPIJSON(w, http.StatusOK, map[string]string{"error": "coach_unavailable"})
			return
		}
		writeAPIJSON(w, http.StatusOK, d.Coach(time.Now()))
	})
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, _ *http.Request) {
		d := s.state()
		if d.Config == nil {
			writeAPIJSON(w, http.StatusOK, map[string]string{"error": "config_unavailable"})
			return
		}
		// Snapshot redacts every secret; the raw Config never leaves.
		snap, err := d.Config.Snapshot()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		writeAPIJSON(w, http.StatusOK, snap)
	})
	mux.HandleFunc("GET /api/data-sources", func(w http.ResponseWriter, r *http.Request) {
		since, until, err := sinceUntil(r)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err)
			return
		}
		d := s.state()
		var health []state.SourceReport
		if d.Health != nil {
			health = d.Health()
		}
		res, err := state.DataSourcesOf(r.Context(), d.Count, health, since, until)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		writeAPIJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("GET /api/vendor-usage", func(w http.ResponseWriter, r *http.Request) {
		hours := 0
		if v := r.URL.Query().Get("window_hours"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeAPIError(w, http.StatusBadRequest, errors.New("window_hours must be a non-negative integer"))
				return
			}
			hours = n
		}
		d := s.state()
		res, err := state.VendorUsageOf(r.Context(), d.Config, d.Count, hours, time.Now())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		writeAPIJSON(w, http.StatusOK, res)
	})
}

// sinceUntil reads ?since= (RFC 3339 or a duration such as 24h; default
// 30 days) and ?until= (RFC 3339; default open).
func sinceUntil(r *http.Request) (time.Time, time.Time, error) {
	since := time.Now().Add(-30 * 24 * time.Hour)
	var until time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			since = time.Now().Add(-d)
		} else if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		} else {
			return since, until, errors.New("since must be RFC 3339 or a duration such as 24h")
		}
	}
	if v := r.URL.Query().Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return since, until, errors.New("until must be RFC 3339")
		}
		until = t
	}
	return since, until, nil
}
