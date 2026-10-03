package proxy

import (
	"context"
	"net/http"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
)

// WithPlans serves plan headroom, the session budget and the glance that
// composes them, from the same capability the MCP tools and the CLI call
// (ADR 0010):
//
//	GET /api/glance
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
