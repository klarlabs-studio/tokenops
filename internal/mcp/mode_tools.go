package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/felixgeelhaar/tokenops/internal/config"
)

// ModeDeps wires the config-mutation tools. ConfigPath empty falls back
// to the default init-managed location.
type ModeDeps struct {
	ConfigPath string
}

func (d ModeDeps) path() (string, error) {
	if d.ConfigPath != "" {
		return d.ConfigPath, nil
	}
	return config.DefaultPath()
}

// restartHint is appended to every mutation response: the MCP serve
// process persists the change, but mode / routing rules / the watcher
// are wired into the daemon at boot.
const restartHint = "persisted; restart the daemon (`tokenops start`) to apply"

type modeInput struct {
	Set string `json:"set,omitempty" jsonschema:"enum=passive,enum=active,description=Omit to read the current mode. passive = analytics only; active = passive + live routing interventions + background spend watcher."`
}

type budgetSetInput struct {
	Name       string  `json:"name" jsonschema:"description=Budget identifier; set upserts by name"`
	Window     string  `json:"window,omitempty" jsonschema:"enum=daily,enum=weekly,enum=monthly,description=Calendar window (UTC)"`
	LimitUSD   float64 `json:"limit_usd,omitempty"`
	WarnAt     float64 `json:"warn_at,omitempty" jsonschema:"description=Fraction of limit_usd for warn alerts (default 0.75)"`
	CritAt     float64 `json:"crit_at,omitempty" jsonschema:"description=Fraction of limit_usd for critical alerts (default 0.95)"`
	WorkflowID string  `json:"workflow_id,omitempty"`
	AgentID    string  `json:"agent_id,omitempty"`
	Delete     bool    `json:"delete,omitempty" jsonschema:"description=Remove the budget with this name"`
}

type routingRuleSetInput struct {
	Provider  string   `json:"provider" jsonschema:"description=Provider the rule applies to (anthropic, openai, gemini, mistral)"`
	FromModel string   `json:"from_model" jsonschema:"description=Model to match; trailing * is a prefix match (e.g. claude-fable-5*)"`
	ToModel   string   `json:"to_model,omitempty" jsonschema:"description=Cheaper target model"`
	Quality   float64  `json:"quality,omitempty" jsonschema:"description=Confidence (0-1] that to_model preserves task quality"`
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
		Description("Get or set the operating mode. passive (default) = collect + analyze on demand. active = passive + interventions: optimizer.routing_rules applied to live proxied traffic and a background watcher evaluating budgets + unpriced models. Setting persists to config.yaml; the daemon applies it on restart.").
		Handler(func(_ context.Context, in modeInput) (string, error) {
			path, err := d.path()
			if err != nil {
				return "", err
			}
			cfg, err := config.ReadMutable(path)
			if err != nil {
				return "", err
			}
			current := cfg.Mode
			if current == "" {
				current = config.ModePassive
			}
			if in.Set == "" {
				return jsonString(map[string]any{
					"mode":          strings.ToLower(current),
					"budgets":       len(cfg.Budgets),
					"routing_rules": len(cfg.Optimizer.RoutingRules),
				}), nil
			}
			cfg.Mode = strings.ToLower(in.Set)
			if err := config.WriteMutable(path, cfg); err != nil {
				return "", err
			}
			return jsonString(map[string]any{
				"mode":   cfg.Mode,
				"config": path,
				"note":   restartHint,
			}), nil
		})

	s.Tool("tokenops_budget_set").
		Description("Create, update (upsert by name), or delete a spend budget (calendar window + USD limit). In active mode the daemon watcher evaluates budgets every watch.interval and logs threshold/forecast breaches. Persists to config.yaml; daemon applies on restart.").
		Handler(func(_ context.Context, in budgetSetInput) (string, error) {
			path, err := d.path()
			if err != nil {
				return "", err
			}
			cfg, err := config.ReadMutable(path)
			if err != nil {
				return "", err
			}
			idx := -1
			for i, b := range cfg.Budgets {
				if b.Name == in.Name {
					idx = i
					break
				}
			}
			switch {
			case in.Delete:
				if idx < 0 {
					return "", errors.New("no budget named " + in.Name)
				}
				cfg.Budgets = append(cfg.Budgets[:idx], cfg.Budgets[idx+1:]...)
			default:
				b := config.BudgetConfig{
					Name: in.Name, Window: strings.ToLower(in.Window), LimitUSD: in.LimitUSD,
					WarnAt: in.WarnAt, CritAt: in.CritAt,
					WorkflowID: in.WorkflowID, AgentID: in.AgentID,
				}
				if idx >= 0 {
					cfg.Budgets[idx] = b
				} else {
					cfg.Budgets = append(cfg.Budgets, b)
				}
			}
			if err := config.WriteMutable(path, cfg); err != nil {
				return "", err
			}
			return jsonString(map[string]any{
				"budgets": cfg.Budgets,
				"config":  path,
				"note":    restartHint,
			}), nil
		})

	s.Tool("tokenops_routing_rule_set").
		Description("Create, update (upsert by provider + from_model), or delete a model-routing rule. Rules show would-be savings in tokenops_replay; with mode=active the proxy rewrites matching live requests to the target model. Persists to config.yaml; daemon applies on restart.").
		Handler(func(_ context.Context, in routingRuleSetInput) (string, error) {
			path, err := d.path()
			if err != nil {
				return "", err
			}
			cfg, err := config.ReadMutable(path)
			if err != nil {
				return "", err
			}
			idx := -1
			for i, r := range cfg.Optimizer.RoutingRules {
				if r.Provider == in.Provider && r.FromModel == in.FromModel {
					idx = i
					break
				}
			}
			switch {
			case in.Delete:
				if idx < 0 {
					return "", errors.New("no routing rule for " + in.Provider + "/" + in.FromModel)
				}
				cfg.Optimizer.RoutingRules = append(
					cfg.Optimizer.RoutingRules[:idx], cfg.Optimizer.RoutingRules[idx+1:]...)
			default:
				r := config.RoutingRuleConfig{
					Provider: in.Provider, FromModel: in.FromModel, ToModel: in.ToModel,
					Quality: in.Quality, Fallbacks: in.Fallbacks,
				}
				if idx >= 0 {
					cfg.Optimizer.RoutingRules[idx] = r
				} else {
					cfg.Optimizer.RoutingRules = append(cfg.Optimizer.RoutingRules, r)
				}
			}
			if err := config.WriteMutable(path, cfg); err != nil {
				return "", err
			}
			return jsonString(map[string]any{
				"routing_rules": cfg.Optimizer.RoutingRules,
				"mode":          cfg.Mode,
				"config":        path,
				"note":          restartHint,
			}), nil
		})

	return nil
}
