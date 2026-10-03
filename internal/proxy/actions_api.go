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
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
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
	// Store receives outcome records; nil answers storage_disabled.
	Store *sqlite.Store
	// Coach supplies the follow-through ledger and the context levers a
	// coach change reports against.
	Coach func() (coachcap.Ledger, coachcap.ContextLevers)
}

// WithActions serves the writes an operator makes from a surface that is
// not an agent, through the same capability the MCP tools call (ADR 0010
// §6):
//
//	POST /api/mode             {"mode": "active"}
//	POST /api/budgets          a budget, upserted by name, or {"name", "delete": true}
//	POST /api/routing/rules    a routing rule, upserted by provider + from_model
//	POST /api/plans            a plan binding
//	POST /api/preferred-models {"provider", "model"} or {"provider", "clear": true}
//	POST /api/routing/decisions {"key", "decision": "approve" | "deny"}
//	POST /api/outcomes         the operator's judgement of an execution
//	POST /api/coach            a preset, or the coach's dials
//
// They are mounted only behind the API token, and take only a JSON body,
// which a browser cannot send cross-origin without a preflight the daemon
// never answers. deps is called per request; nil leaves them unmounted.
func WithActions(deps func() ActionDeps) Option {
	return func(s *Server) { s.actions = deps }
}

// The action request bodies. Each is the JSON a route takes; the OpenAPI
// document is generated from them.

// ModeRequest sets the operating mode: passive or active.
type ModeRequest struct {
	Mode string `json:"mode"`
}

// BudgetRequest creates or updates a budget by name, or deletes it.
type BudgetRequest struct {
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

// RoutingRuleRequest creates or updates the routing rule for a provider and source model, or deletes it.
type RoutingRuleRequest struct {
	Provider  string   `json:"provider"`
	FromModel string   `json:"from_model"`
	ToModel   string   `json:"to_model"`
	Quality   float64  `json:"quality"`
	Fallbacks []string `json:"fallbacks"`
	Delete    bool     `json:"delete"`
}

// PlanRequest binds a provider to a plan, or clears its binding.
type PlanRequest struct {
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

// PreferredModelRequest sets or clears the model a provider's routing may not move above.
type PreferredModelRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Clear    bool   `json:"clear"`
}

// RoutingDecisionRequest answers a pending routing proposal: approve or deny.
type RoutingDecisionRequest struct {
	Key      string `json:"key"`
	Decision string `json:"decision"`
}

// OutcomeRequest records the operator's judgement of an execution: achieved, partial or not_achieved.
type OutcomeRequest struct {
	ExecutionID      string   `json:"execution_id"`
	DecisionID       string   `json:"decision_id"`
	Result           string   `json:"result"`
	Caveat           string   `json:"caveat"`
	AttentionMinutes *float64 `json:"attention_minutes"`
}

// maxActionBody bounds a request body; the largest action is a few
// hundred bytes.
const maxActionBody = 64 << 10

func (s *Server) registerActionRoutes(mux RouteMux) {
	// A write is never served without the token, whatever else is wired.
	if s.actions == nil || s.dashAuth == nil {
		return
	}
	mux.HandleFunc("POST /api/mode", s.action("mode", restart, func(r *http.Request, path string) (any, error) {
		var in ModeRequest
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		return actions.SetMode(path, in.Mode)
	}))
	mux.HandleFunc("POST /api/budgets", s.action("budget", restart, func(r *http.Request, path string) (any, error) {
		var in BudgetRequest
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		return actions.SetBudget(path, actions.BudgetRequest{Delete: in.Delete, BudgetUpdate: config.BudgetUpdate{
			Name: in.Name, Window: in.Window, LimitUSD: in.LimitUSD, LimitTokens: in.LimitTokens,
			WarnAt: in.WarnAt, CritAt: in.CritAt, WorkflowID: in.WorkflowID, AgentID: in.AgentID, Basis: in.Basis,
		}})
	}))
	mux.HandleFunc("POST /api/routing/rules", s.action("routing_rule", restart, func(r *http.Request, path string) (any, error) {
		var in RoutingRuleRequest
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		return actions.SetRoutingRule(path, actions.RoutingRuleRequest{
			Provider: in.Provider, FromModel: in.FromModel, ToModel: in.ToModel,
			Quality: in.Quality, Fallbacks: in.Fallbacks, Delete: in.Delete,
		})
	}))
	mux.HandleFunc("POST /api/plans", s.action("plan", restart, func(r *http.Request, path string) (any, error) {
		var in PlanRequest
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		return actions.SetPlan(r.Context(), path, actions.PlanRequest{
			Provider: in.Provider, Plan: in.Plan, SpendLimitUSD: in.SpendLimitUSD, LimitWindow: in.LimitWindow,
			RateFactor: in.RateFactor, Clear: in.Clear, Price: in.Price, Currency: in.Currency,
			Since: in.Since, Actor: "api",
		}, time.Now().UTC())
	}))
	mux.HandleFunc("POST /api/preferred-models", s.action("preferred_model", restart, func(r *http.Request, path string) (any, error) {
		var in PreferredModelRequest
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		return actions.SetPreferredModel(path, in.Provider, in.Model, in.Clear)
	}))
	mux.HandleFunc("POST /api/routing/decisions", s.action("routing_decision", noRestart, func(r *http.Request, _ string) (any, error) {
		var in RoutingDecisionRequest
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		return actions.DecideRouting("", in.Key, in.Decision)
	}))
	mux.HandleFunc("POST /api/outcomes", s.action("outcome", noRestart, func(r *http.Request, _ string) (any, error) {
		var in OutcomeRequest
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		return actions.RecordOutcome(r.Context(), s.actions().Store, actions.OutcomeRequest{
			ExecutionID: in.ExecutionID, DecisionID: in.DecisionID, Result: in.Result,
			Caveat: in.Caveat, AttentionMinutes: in.AttentionMinutes,
		})
	}))
	mux.HandleFunc("POST /api/coach", s.action("coach", noRestart, func(r *http.Request, path string) (any, error) {
		var in coachcap.ChangeRequest
		if err := decodeAction(r, &in); err != nil {
			return nil, err
		}
		if in.Empty() {
			return nil, actions.InputError{Err: errors.New("name a preset, or the dials to change")}
		}
		var (
			ledger coachcap.Ledger
			levers coachcap.ContextLevers
		)
		if env := s.actions().Coach; env != nil {
			ledger, levers = env()
		}
		report, err := coachcap.Change(r.Context(), path, ledger, levers, time.Now(), in)
		switch {
		case err == nil:
			return report, nil
		case in.Preset != "" && !errors.Is(err, coachcap.ErrPreset):
			return nil, err // the preset could not be run at all
		default:
			return nil, actions.InputError{Err: err} // a value the coach refused
		}
	}))
}

// action runs do, records it in the audit log, answers with the change and
// the note saying whether it is live, and only then lets the change take
// effect.
// Whether an action needs the daemon restarted to take effect.
const (
	restart   = true
	noRestart = false
)

func (s *Server) action(target string, needsRestart bool, do func(r *http.Request, path string) (any, error)) http.HandlerFunc {
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
		body, err := withDefault(change, "note", "")
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
		switch {
		case needsRestart && d.Apply != nil:
			body["note"], after = d.Apply()
		case body["note"] == "":
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

// withDefault renders v as a JSON object, with key set to val unless v
// already has it.
func withDefault(v any, key string, val any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if _, ok := m[key]; !ok {
		m[key] = val
	}
	return m, nil
}
