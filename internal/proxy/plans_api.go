package proxy

import (
	"context"
	"net/http"
	"time"

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
		return g.Payload(), nil
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
		return findings.Compute(findings.Gather(&g, c, findings.DefaultDir())), nil
	}))
	mux.HandleFunc("GET /api/plans/headroom", serve(func(ctx context.Context, d headroom.Deps, now time.Time) (any, error) {
		res, err := headroom.Compute(ctx, d, now)
		if err != nil {
			return nil, err
		}
		return res.Payload(), nil
	}))
	mux.HandleFunc("GET /api/plans/session-budget", serve(func(ctx context.Context, d headroom.Deps, now time.Time) (any, error) {
		res, err := headroom.SessionBudgets(ctx, d, now)
		if err != nil {
			return nil, err
		}
		return res.Payload(), nil
	}))
}
