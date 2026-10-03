// Package actions changes what TokenOps does: the operating mode, budgets,
// routing rules and plan bindings. The MCP tools and the daemon API both
// call it (ADR 0010 §6), so asking an agent and pressing a button in a
// menu bar write the same config the same way.
//
// An action reads config.yaml, changes it, and writes it back. Making the
// change take effect (restarting the supervised daemon) is left to the
// caller, because it differs: the MCP server restarts the daemon, and the
// daemon cannot restart itself until it has answered the request.
package actions

import (
	"errors"
	"fmt"

	"go.klarlabs.de/tokenops/internal/config"
)

// InputError is a request the caller got wrong: a bad value, a missing
// field, a name that does not exist. Adapters answer it as such (a 400, an
// MCP input error) rather than as a failure of TokenOps.
type InputError struct{ Err error }

func (e InputError) Error() string { return e.Err.Error() }
func (e InputError) Unwrap() error { return e.Err }

func inputErr(err error) error {
	if err == nil {
		return nil
	}
	return InputError{Err: err}
}

// IsInput reports whether err is the caller's.
func IsInput(err error) bool {
	var ie InputError
	return errors.As(err, &ie)
}

// edit reads the config at path, applies change, and writes it back. A
// change that fails leaves the file untouched.
func edit(path string, change func(*config.Config) error) (config.Config, error) {
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return config.Config{}, inputErr(err)
	}
	if err := change(&cfg); err != nil {
		return config.Config{}, err
	}
	if err := config.WriteMutable(path, cfg); err != nil {
		return config.Config{}, inputErr(err)
	}
	return cfg, nil
}

// ModeChange is the mode after a change.
type ModeChange struct {
	Mode   string `json:"mode"`
	Config string `json:"config"`
	// Active reports whether the new mode needs a running daemon.
	Active bool `json:"-"`
}

// SetMode sets the operating mode: passive or active.
func SetMode(path, mode string) (ModeChange, error) {
	m, err := config.ParseMode(mode)
	if err != nil {
		return ModeChange{}, inputErr(err)
	}
	cfg, err := edit(path, func(c *config.Config) error {
		c.Mode = m
		return nil
	})
	if err != nil {
		return ModeChange{}, err
	}
	return ModeChange{Mode: cfg.Mode, Config: path, Active: cfg.ActiveMode()}, nil
}

// BudgetRequest creates, updates or deletes a budget.
type BudgetRequest struct {
	config.BudgetUpdate
	Delete bool
}

// BudgetChange is every budget after a change.
type BudgetChange struct {
	Budgets []config.BudgetConfig `json:"budgets"`
	Config  string                `json:"config"`
}

// SetBudget upserts a budget by name, or deletes it. An update changes only
// the fields given.
func SetBudget(path string, req BudgetRequest) (BudgetChange, error) {
	cfg, err := edit(path, func(c *config.Config) error {
		if req.Delete {
			if !c.RemoveBudget(req.Name) {
				return inputErr(errors.New("no budget named " + req.Name))
			}
			return nil
		}
		_, err := c.UpsertBudget(req.BudgetUpdate)
		return inputErr(err)
	})
	if err != nil {
		return BudgetChange{}, err
	}
	return BudgetChange{Budgets: cfg.Budgets, Config: path}, nil
}

// RoutingRuleRequest creates, updates or deletes the rule for a provider
// and source model.
type RoutingRuleRequest struct {
	Provider, FromModel, ToModel string
	Quality                      float64
	Fallbacks                    []string
	Delete                       bool
}

// RoutingRuleChange is every routing rule after a change.
type RoutingRuleChange struct {
	RoutingRules []config.RoutingRuleConfig `json:"routing_rules"`
	Mode         string                     `json:"mode"`
	Config       string                     `json:"config"`
}

// SetRoutingRule upserts the rule for req's provider and source model, or
// deletes it. A new rule is validated as given, before the file is
// touched, so a refusal names the argument rather than an index into
// config.yaml.
func SetRoutingRule(path string, req RoutingRuleRequest) (RoutingRuleChange, error) {
	rule := config.RoutingRuleConfig{
		Provider: req.Provider, FromModel: req.FromModel, ToModel: req.ToModel,
		Quality: req.Quality, Fallbacks: req.Fallbacks,
	}
	if !req.Delete {
		if err := rule.Validate(); err != nil {
			return RoutingRuleChange{}, inputErr(err)
		}
	}
	cfg, err := edit(path, func(c *config.Config) error {
		idx := -1
		for i, r := range c.Optimizer.RoutingRules {
			if r.Provider == req.Provider && r.FromModel == req.FromModel {
				idx = i
				break
			}
		}
		switch {
		case req.Delete && idx < 0:
			return inputErr(fmt.Errorf("no routing rule for %s/%s", req.Provider, req.FromModel))
		case req.Delete:
			c.Optimizer.RoutingRules = append(c.Optimizer.RoutingRules[:idx], c.Optimizer.RoutingRules[idx+1:]...)
		case idx >= 0:
			c.Optimizer.RoutingRules[idx] = rule
		default:
			c.Optimizer.RoutingRules = append(c.Optimizer.RoutingRules, rule)
		}
		return nil
	})
	if err != nil {
		return RoutingRuleChange{}, err
	}
	return RoutingRuleChange{RoutingRules: cfg.Optimizer.RoutingRules, Mode: cfg.Mode, Config: path}, nil
}
