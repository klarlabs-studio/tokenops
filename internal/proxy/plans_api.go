package proxy

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/budgets"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
)

// WithPlans serves plan headroom, the session budget and the glance that
// composes them, from the same capability the MCP tools and the CLI call
// (ADR 0010):
//
//	GET /api/glance
//	GET /api/findings
//	GET /api/plans/headroom
//	GET /api/plans/session-budget
//	GET /api/plans/advice?days=
//
// deps is called per request. nil leaves the routes unmounted.
func WithPlans(deps func() headroom.Deps) Option {
	return func(s *Server) { s.plans = deps }
}

func (s *Server) registerPlanRoutes(mux RouteMux) {
	if s.plans == nil {
		return
	}
	serve := func(answer func(context.Context, headroom.Deps, time.Time) (any, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, err := answer(r.Context(), s.plans(), time.Now().UTC())
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, err)
				return
			}
			writeAPIJSON(w, http.StatusOK, body)
		}
	}
	mux.HandleFunc("GET /api/glance", serve(func(ctx context.Context, d headroom.Deps, now time.Time) (any, error) {
		g, err := headroom.ComputeGlance(ctx, d, now)
		if err != nil {
			return nil, err
		}
		p := g.Payload()
		p.Boundaries = boundaries.GlanceAdvice(d.Config, g.Headroom.Reports)
		p.SpendBudgets = budgets.Current("", now)
		return p, nil
	}))
	// The findings rank what the coach and the session analysis observed
	// around the same glance; the coach's report comes from the state
	// routes' deps when they are mounted.
	mux.HandleFunc("GET /api/findings", serve(func(ctx context.Context, d headroom.Deps, now time.Time) (any, error) {
		g, err := headroom.ComputeGlance(ctx, d, now)
		if err != nil {
			return nil, err
		}
		var c *coachcap.Report
		if s.state != nil {
			if st := s.state(); st.Coach != nil {
				r := st.Coach(now)
				c = &r
			}
		}
		return findings.Compute(findings.Gather(&g, c, d.Config, findings.DefaultDir())), nil
	}))
	mux.HandleFunc("GET /api/plans/headroom", serve(func(ctx context.Context, d headroom.Deps, now time.Time) (any, error) {
		res, err := headroom.Compute(ctx, d, now)
		if err != nil {
			return nil, err
		}
		return res.Payload(), nil
	}))
	// Plan right-sizing: advice only, it binds nothing.
	mux.HandleFunc("GET /api/plans/advice", func(w http.ResponseWriter, r *http.Request) {
		var q headroom.AdviceQuery
		if raw := r.URL.Query().Get("days"); raw != "" {
			days, err := strconv.Atoi(raw)
			if err != nil || days < 1 || days > headroom.MaxAdviceDays {
				writeAPIError(w, http.StatusBadRequest, fmt.Errorf("days must be a whole number from 1 to %d", headroom.MaxAdviceDays))
				return
			}
			q.Days = days
		}
		res, err := headroom.Advise(r.Context(), s.plans(), q, time.Now().UTC())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		writeAPIJSON(w, http.StatusOK, res.Payload())
	})
	mux.HandleFunc("GET /api/plans/session-budget", serve(func(ctx context.Context, d headroom.Deps, now time.Time) (any, error) {
		res, err := headroom.SessionBudgets(ctx, d, now)
		if err != nil {
			return nil, err
		}
		return res.Payload(), nil
	}))
}
