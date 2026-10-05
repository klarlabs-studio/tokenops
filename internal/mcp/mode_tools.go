package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"go.klarlabs.de/tokenops/internal/capability/actions"

	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/config"
)

// ModeDeps wires the config-mutation tools. ConfigPath empty falls back
// to the default init-managed location.
type ModeDeps struct {
	ConfigPath string
	// ApplyConfig makes a written config take effect and returns what
	// happened; see applyConfig.
	ApplyConfig func() string
	// StartDaemon launches the daemon when activating active mode and
	// none is running. nil uses the default detached spawn of the
	// current executable; tests inject a fake.
	StartDaemon func(configPath string) (pid int, logPath string, err error)
	// DaemonURL is the configured listen address as a URL (see
	// ConfiguredDaemonURL), probed when the daemon's URL hint is missing
	// or stale. Empty skips that probe.
	DaemonURL string
	// UnitInstalled reports whether a launchd/systemd unit supervises the
	// daemon. When it does, activating active mode restarts the unit
	// instead of ever spawning a daemon beside it. nil reads as no unit;
	// serve always wires it.
	UnitInstalled func() bool
}

func (d ModeDeps) path() (string, error) {
	if d.ConfigPath != "" {
		return d.ConfigPath, nil
	}
	return config.DefaultPath()
}

// applyConfig makes a config write take effect and says what happened. The
// daemon reads config once, at boot, so a write it has not re-read has not
// changed anything. These tools used to answer "restart the daemon
// (`tokenops start`)" — a manual step, and on a supervised machine the
// wrong one, since `tokenops start` starts a second daemon. serve wires
// ApplyConfig to restart the supervised daemon; nil (tests) only reports.
func applyConfig(apply func() string) string {
	if apply == nil {
		return "written; restart the daemon for it to take effect: `tokenops daemon restart`"
	}
	return apply()
}

type modeInput struct {
	Set string `json:"set,omitempty" jsonschema:"enum=passive,enum=active,description=Omit to read the current mode. passive = analytics only; active = passive + live routing interventions + background spend watcher."`
}

// budgetSetInput edits one budget. Omitted fields keep the stored budget's
// values, so an edit names only what it changes.
type budgetSetInput struct {
	Name        string  `json:"name" jsonschema:"description=Budget identifier; set upserts by name"`
	Window      string  `json:"window,omitempty" jsonschema:"enum=daily,enum=weekly,enum=monthly,description=Calendar window (UTC); a new budget defaults to monthly"`
	LimitUSD    float64 `json:"limit_usd,omitempty" jsonschema:"description=Ceiling in USD, for basis spend or equivalent"`
	LimitTokens int64   `json:"limit_tokens,omitempty" jsonschema:"description=Ceiling in tokens, for basis tokens"`
	WarnAt      float64 `json:"warn_at,omitempty" jsonschema:"description=Fraction of the limit for warn alerts (default 0.75)"`
	CritAt      float64 `json:"crit_at,omitempty" jsonschema:"description=Fraction of the limit for critical alerts (default 0.95)"`
	WorkflowID  string  `json:"workflow_id,omitempty"`
	AgentID     string  `json:"agent_id,omitempty"`
	Basis       string  `json:"basis,omitempty" jsonschema:"enum=spend,enum=equivalent,enum=tokens,description=What the limit watches: spend (real billed cost, default), equivalent (API list-price value incl. plan-covered usage), or tokens (raw token volume against limit_tokens). On flat plans real spend is ~0, so use tokens or equivalent."`
	Delete      bool    `json:"delete,omitempty" jsonschema:"description=Remove the budget with this name"`
}

func (in budgetSetInput) update() config.BudgetUpdate {
	return config.BudgetUpdate{
		Name: in.Name, Window: in.Window,
		LimitUSD: in.LimitUSD, LimitTokens: in.LimitTokens,
		WarnAt: in.WarnAt, CritAt: in.CritAt,
		WorkflowID: in.WorkflowID, AgentID: in.AgentID,
		Basis: in.Basis,
	}
}

type routingRuleSetInput struct {
	Provider  string   `json:"provider" jsonschema:"description=Provider the rule applies to (anthropic, openai, gemini, mistral)"`
	FromModel string   `json:"from_model" jsonschema:"description=Model to match; trailing * is a prefix match (e.g. claude-fable-5*)"`
	ToModel   string   `json:"to_model,omitempty" jsonschema:"description=Cheaper target model. Required unless delete."`
	Quality   float64  `json:"quality,omitempty" jsonschema:"description=Confidence (0-1] that to_model preserves task quality. Required unless delete."`
	Fallbacks []string `json:"fallbacks,omitempty"`
	Delete    bool     `json:"delete,omitempty" jsonschema:"description=Remove the rule matching provider + from_model"`
}

// RegisterModeTools adds config-mutation tools: operating mode, budget
// limits, and routing rules. Mutations write the same config.yaml the
// CLI verbs (`tokenops plan set`, `tokenops init`) manage; validation
// runs before every write so a bad call cannot corrupt the file.
func RegisterModeTools(s *Server, d ModeDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}

	s.Tool("tokenops_mode").
		Description("Get or set the operating mode. passive (default) = collect + analyze on demand. active = passive + interventions: optimizer.routing_rules applied to live proxied traffic and a background watcher evaluating budgets + unpriced models. Setting persists to config.yaml and restarts a supervised daemon so it takes effect; with no daemon anywhere, activating starts one.").
		Handler(func(_ context.Context, in modeInput) (string, error) {
			// Checked before any read or write, with the rule the
			// terminal's `mode` uses, so a bad value is refused by name
			// rather than as an invalid config after the fact.
			var want string
			if in.Set != "" {
				m, err := config.ParseMode(in.Set)
				if err != nil {
					return "", inputError(err)
				}
				want = m
			}
			path, err := d.path()
			if err != nil {
				return "", inputError(err)
			}
			if want == "" {
				cfg, err := config.ReadMutable(path)
				if err != nil {
					return "", inputError(err)
				}
				return jsonString(modeAuthorityPayload(cfg)), nil
			}
			change, err := actions.SetMode(path, want)
			if err != nil {
				return "", actionError(err)
			}
			resp := map[string]any{"mode": change.Mode, "config": change.Config}
			// Active mode without a daemon is a no-op — the proxy
			// (routing) and the watcher live there. Ensure one runs
			// and has read the new mode.
			if change.Active {
				resp["daemon"] = d.ensureDaemon(path)
			} else {
				resp["note"] = applyConfig(d.ApplyConfig)
			}
			return jsonString(resp), nil
		})

	s.Tool("tokenops_budget_set").
		Description("Create, update (upsert by name), or delete a budget: a calendar window plus a ceiling — limit_usd for basis spend/equivalent, limit_tokens for basis tokens. An update changes only the fields given; the rest of the stored budget is kept. A budget without a ceiling is refused. In active mode the daemon watcher evaluates budgets every watch.interval and logs threshold/forecast breaches. Persists to config.yaml and restarts a supervised daemon so it takes effect.").
		Handler(func(_ context.Context, in budgetSetInput) (string, error) {
			path, err := d.path()
			if err != nil {
				return "", inputError(err)
			}
			change, err := actions.SetBudget(path, actions.BudgetRequest{BudgetUpdate: in.update(), Delete: in.Delete})
			if err != nil {
				return "", actionError(err)
			}
			return withNote(change, applyConfig(d.ApplyConfig)), nil
		})

	s.Tool("tokenops_routing_rule_set").
		Description("Create, update (upsert by provider + from_model), or delete a model-routing rule. Rules show would-be savings in `tokenops replay`; with mode=active the proxy rewrites matching live requests to the target model. Persists to config.yaml and restarts a supervised daemon so it takes effect.").
		Handler(func(_ context.Context, in routingRuleSetInput) (string, error) {
			path, err := d.path()
			if err != nil {
				return "", inputError(err)
			}
			change, err := actions.SetRoutingRule(path, actions.RoutingRuleRequest{
				Provider: in.Provider, FromModel: in.FromModel, ToModel: in.ToModel,
				Quality: in.Quality, Fallbacks: in.Fallbacks, Delete: in.Delete,
			})
			if err != nil {
				return "", actionError(err)
			}
			return withNote(change, applyConfig(d.ApplyConfig)), nil
		})

	return nil
}

// modeAuthorityPayload answers "what can TokenOps do without me", in the
// shape `tokenops mode` prints.
//
// The tool used to return the daemon's rung plus counts of budgets and
// routing rules, which answers "which of the five settings did I last
// touch". The other four, in three other vocabularies, were absent — and
// an agent deciding whether to propose or to act was reading one fifth
// of the answer.
//
// Both surfaces read the same capability so they cannot drift into
// disagreeing about what the operator's own machine is allowed to do.
func modeAuthorityPayload(cfg config.Config) state.Mode {
	return state.ModeOf(cfg)
}

// actionError answers a request the caller got wrong as an input error and
// anything else as a failure.
func actionError(err error) error {
	if actions.IsInput(err) {
		return inputError(err)
	}
	return err
}

// withNote renders a change with the note saying whether it is live.
func withNote(change any, note string) string {
	b, err := json.Marshal(change)
	if err != nil {
		return jsonString(map[string]any{"error": err.Error()})
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return jsonString(map[string]any{"error": err.Error()})
	}
	m["note"] = note
	return jsonString(m)
}
