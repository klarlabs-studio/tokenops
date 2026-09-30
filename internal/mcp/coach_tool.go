package mcp

import (
	"context"
	"errors"
	"strings"
	"time"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/compactlever"
	"go.klarlabs.de/tokenops/internal/infra/followthrough"
)

type coachInput struct {
	Autonomy  string `json:"autonomy,omitempty" jsonschema:"description=Set every power's default autonomy: off | advise | ask | autonomous."`
	Verbosity string `json:"verbosity,omitempty" jsonschema:"description=Set how much the coach says: quiet | normal | verbose."`
	Power     string `json:"power,omitempty" jsonschema:"description=Power to override: inform | waste | models. Requires rung."`
	Rung      string `json:"rung,omitempty" jsonschema:"description=Autonomy for the named power: off | advise | ask | autonomous."`
}

// RegisterCoachTool registers tokenops_coach: the coach's two dials and
// each power's configured and effective autonomy (ADR 0006), readable and
// settable. It reuses ModeDeps for the config path.
func RegisterCoachTool(s *Server, d ModeDeps) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}
	s.Tool("tokenops_coach").
		Description("Show or set the coach (ADR 0006). With no arguments, returns verbosity and, for each power (inform: quota and context tips; waste: redundant re-reads; models: moving work to a cheaper model), the configured autonomy, the effective autonomy on this machine, the reason they differ, and the setting it came from. autonomy sets every power's default (off | advise | ask | autonomous); power + rung overrides one (context: when agents compact; autonomous sets it in Claude Code, Codex, and opencode settings and restores them when lowered); verbosity sets how much it says (quiet | normal | verbose). A rung the coach cannot deliver yet is reported one rung lower with the reason. follow_through reports, per kind, how often advice was followed or ignored and whether autonomous moves stood or were undone; advice ignored repeatedly is marked quiet and no longer offered unless verbosity is verbose.").
		OutputSchema(coachcap.Report{}).
		Handler(func(_ context.Context, in coachInput) (*coachcap.Report, error) {
			path, err := d.path()
			if err != nil {
				return nil, inputError(err)
			}
			var ledger coachcap.Ledger
			if l, err := followthrough.Default(); err == nil {
				ledger = l
			}
			now := time.Now()
			if in.Autonomy == "" && in.Verbosity == "" && in.Power == "" && in.Rung == "" {
				cfg, err := config.ReadMutable(path)
				if err != nil {
					return nil, inputError(err)
				}
				r := coachcap.Status(cfg, ledger, contextLevers(), now)
				return &r, nil
			}
			if (in.Power == "") != (in.Rung == "") {
				return nil, inputError(errors.New("power and rung go together"))
			}
			r, err := coachcap.Apply(path, ledger, contextLevers(), now, func(cfg *config.Config) {
				if in.Autonomy != "" {
					cfg.Coach.Autonomy = in.Autonomy
				}
				if in.Verbosity != "" {
					cfg.Coach.Verbosity = in.Verbosity
				}
				if in.Power != "" {
					if cfg.Coach.Powers == nil {
						cfg.Coach.Powers = map[string]string{}
					}
					cfg.Coach.Powers[strings.ToLower(strings.TrimSpace(in.Power))] = in.Rung
				}
			})
			if err != nil {
				return nil, inputError(err)
			}
			return &r, nil
		})
	return nil
}

// contextLevers is the context power's port over the agents' settings.
func contextLevers() coachcap.ContextLevers {
	var cfg config.Config
	if path, err := config.DefaultPath(); err == nil {
		if loaded, err := config.Load(path); err == nil {
			cfg = loaded
		}
	}
	l, err := compactlever.New(cfg)
	if err != nil {
		return nil
	}
	return l
}
