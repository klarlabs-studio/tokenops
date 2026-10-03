package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	"go.klarlabs.de/tokenops/internal/config"
)

// ActionDeps is what the action routes need from the daemon.
type ActionDeps struct {
	// ConfigPath is the file the daemon was started with.
	ConfigPath string
	// Apply makes a written change take effect. It returns the note to
	// answer with and, when the work must wait until the answer is sent
	// (the daemon restarting itself), a function to run afterwards.
	Apply func() (note string, after func())
	// Audit records a change in the audit log.
	Audit func(ctx context.Context, target string, details map[string]any)
}

// WithActions serves the writes an operator makes from a surface that is
// not an agent, through the same capability the MCP tools call (ADR 0010
// §6):
//
//	POST /api/mode             {"mode": "active"}
//	POST /api/budgets          a budget, upserted by name, or {"name", "delete": true}
//	POST /api/routing/rules    a routing rule, upserted by provider + from_model
//	POST /api/plans            a plan binding
//
// They are mounted only behind the API token, and take only a JSON body,
// which a browser cannot send cross-origin without a preflight the daemon
// never answers. deps is called per request; nil leaves them unmounted.
func WithActions(deps func() ActionDeps) Option {
	return func(s *Server) { s.actions = deps }
}

// maxActionBody bounds a request body; the largest action is a few
// hundred bytes.
const maxActionBody = 64 << 10

func (s *Server) registerActionRoutes(mux *http.ServeMux) {
	// A write is never served without the token, whatever else is wired.
	if s.actions == nil || s.dashAuth == nil {
		return
	}
	mux.HandleFunc("POST /api/mode", s.action("mode", func(r *http.Request, path string) (any, error) {
		var in struct {
			Mode string `json:"mode"`
		}
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		return actions.SetMode(path, in.Mode)
	}))
	mux.HandleFunc("POST /api/budgets", s.action("budget", func(r *http.Request, path string) (any, error) {
		var in struct {
			Name        string  `json:"name"`
			Window      string  `json:"window"`
			LimitUSD    float64 `json:"limit_usd"`
			LimitTokens int64   `json:"limit_tokens"`
			WarnAt      float64 `json:"warn_at"`
			CritAt      float64 `json:"crit_at"`
			WorkflowID  string  `json:"workflow_id"`
			AgentID     string  `json:"agent_id"`
			Basis       string  `json:"basis"`
			Delete      bool    `json:"delete"`
		}
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		return actions.SetBudget(path, actions.BudgetRequest{Delete: in.Delete, BudgetUpdate: config.BudgetUpdate{
			Name: in.Name, Window: in.Window, LimitUSD: in.LimitUSD, LimitTokens: in.LimitTokens,
			WarnAt: in.WarnAt, CritAt: in.CritAt, WorkflowID: in.WorkflowID, AgentID: in.AgentID, Basis: in.Basis,
		}})
	}))
	mux.HandleFunc("POST /api/routing/rules", s.action("routing_rule", func(r *http.Request, path string) (any, error) {
		var in struct {
			Provider  string   `json:"provider"`
			FromModel string   `json:"from_model"`
			ToModel   string   `json:"to_model"`
			Quality   float64  `json:"quality"`
			Fallbacks []string `json:"fallbacks"`
			Delete    bool     `json:"delete"`
		}
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		return actions.SetRoutingRule(path, actions.RoutingRuleRequest{
			Provider: in.Provider, FromModel: in.FromModel, ToModel: in.ToModel,
			Quality: in.Quality, Fallbacks: in.Fallbacks, Delete: in.Delete,
		})
	}))
	mux.HandleFunc("POST /api/plans", s.action("plan", func(r *http.Request, path string) (any, error) {
		var in struct {
			Provider      string  `json:"provider"`
			Plan          string  `json:"plan"`
			SpendLimitUSD float64 `json:"spend_limit_usd"`
			LimitWindow   string  `json:"limit_window"`
			RateFactor    float64 `json:"rate_factor"`
			Clear         bool    `json:"clear"`
			Price         float64 `json:"price"`
			Currency      string  `json:"currency"`
			Since         string  `json:"since"`
		}
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		return actions.SetPlan(r.Context(), path, actions.PlanRequest{
			Provider: in.Provider, Plan: in.Plan, SpendLimitUSD: in.SpendLimitUSD, LimitWindow: in.LimitWindow,
			RateFactor: in.RateFactor, Clear: in.Clear, Price: in.Price, Currency: in.Currency,
			Since: in.Since, Actor: "api",
		}, time.Now().UTC())
	}))
}

// action runs do, records it in the audit log, answers with the change and
// the note saying whether it is live, and only then lets the change take
// effect.
func (s *Server) action(target string, do func(r *http.Request, path string) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d := s.actions()
		change, err := do(r, d.ConfigPath)
		switch {
		case actions.IsInput(err):
			writeAPIError(w, http.StatusBadRequest, err)
			return
		case err != nil:
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		body, err := withField(change, "note", "")
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err)
			return
		}
		if d.Audit != nil {
			details := map[string]any{}
			for k, v := range body {
				details[k] = v
			}
			d.Audit(r.Context(), target, details)
		}
		var after func()
		if d.Apply != nil {
			body["note"], after = d.Apply()
		} else {
			delete(body, "note")
		}
		writeAPIJSON(w, http.StatusOK, body)
		if after != nil {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			go after()
		}
	}
}

// decodeAction reads a JSON body into in, refusing anything else: another
// content type, an oversized body, or a field the action does not take.
func decodeAction(r *http.Request, in any) error {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return actions.InputError{Err: errors.New("send the body as application/json")}
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxActionBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(in); err != nil {
		return actions.InputError{Err: fmt.Errorf("body: %w", err)}
	}
	return nil
}

// withField renders v as a JSON object with one more field.
func withField(v any, key string, val any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	m[key] = val
	return m, nil
}
