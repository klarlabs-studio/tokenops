package mcp

import (
	"context"
	"errors"
	"time"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/compactlever"
	"go.klarlabs.de/tokenops/internal/infra/followthrough"
)

type coachInput struct {
	Preset    string `json:"preset,omitempty" jsonschema:"description=Set the whole coach in one choice and wire everything it needs (hooks on every installed agent, where agents compact): observe | advise | guided | autopilot. Other fields are ignored when set."`
	Autonomy  string `json:"autonomy,omitempty" jsonschema:"description=Set every power's default autonomy: off | advise | ask | autonomous."`
	Verbosity string `json:"verbosity,omitempty" jsonschema:"description=Set how much the coach says: quiet | normal | verbose."`
	Power     string `json:"power,omitempty" jsonschema:"description=Power to override: inform | waste | models. Requires rung."`
	Rung      string `json:"rung,omitempty" jsonschema:"description=Autonomy for the named power: off | advise | ask | autonomous."`
	Tip       string `json:"tip,omitempty" jsonschema:"description=Answer a tip, only on the operator's word: acknowledged (seen; nothing more of that kind this session) | expected (normal for them; also tunes the session budget) | snoozed (tips of that kind wait snooze_minutes, default 240). Answers the newest unanswered tip unless tip_id is set."`
	TipID     string `json:"tip_id,omitempty" jsonschema:"description=The tip to answer; empty answers the newest unanswered one."`
	SnoozeMin int    `json:"snooze_minutes,omitempty" jsonschema:"description=How long a snooze lasts, in minutes (default 240)."`
}

// RegisterCoachTool registers tokenops_coach: the coach's two dials and
// each power's configured and effective autonomy (ADR 0006), readable and
// settable. It reuses ModeDeps for the config path.
func RegisterCoachTool(s *Server, d ModeDeps) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}
	s.Tool("tokenops_coach").
		Description("Show or set the coach (ADR 0006). preset sets everything in one opinionated choice (observe: records only; advise: tells, changes nothing; guided: refuses redundant re-reads, asks before moving subagents; autopilot: acts on its own and stays quiet, including where agents compact) and installs the hooks it needs on every installed agent; prefer it when asked to configure tokenops. With no arguments, returns verbosity and, for each power (inform: quota and context tips; waste: redundant re-reads; models: moving work to a cheaper model), the configured autonomy, the effective autonomy on this machine, the reason they differ, and the setting it came from. autonomy sets every power's default (off | advise | ask | autonomous); power + rung overrides one (context: when agents compact; autonomous sets it in Claude Code, Codex, and opencode settings and restores them when lowered); verbosity sets how much it says (quiet | normal | verbose). A rung the coach cannot deliver yet is reported one rung lower with the reason. follow_through reports, per kind, how often advice was followed or ignored and whether autonomous moves stood or were undone; a tip nothing shows the operator saw (delivered only as the Stop hook message, never shown in the status line) and not acted on counts as unseen, neither followed nor ignored; advice ignored repeatedly is marked quiet and no longer offered unless verbosity is verbose. tip answers the coach's newest tip on the operator's word (acknowledged, expected, snoozed): an answered episode is not told again, a snooze holds that kind of tip for a while, and expected answers move the session budget up.").
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
			if in.Tip != "" {
				a, err := coachcap.AnswerTip(ledger, now, in.TipID, in.Tip, time.Duration(in.SnoozeMin)*time.Minute)
				if err != nil {
					return nil, inputError(err)
				}
				cfg, err := config.ReadMutable(path)
				if err != nil {
					return nil, inputError(err)
				}
				r := coachcap.Status(cfg, ledger, contextLevers(), now)
				r.Answered = &a
				return &r, nil
			}
			req := coachcap.ChangeRequest{Preset: in.Preset, Autonomy: in.Autonomy, Verbosity: in.Verbosity, Power: in.Power, Rung: in.Rung}
			if req.Empty() {
				cfg, err := config.ReadMutable(path)
				if err != nil {
					return nil, inputError(err)
				}
				r := coachcap.Status(cfg, ledger, contextLevers(), now)
				return &r, nil
			}
			r, err := coachcap.Change(context.Background(), path, ledger, contextLevers(), now, req)
			if err != nil && !errors.Is(err, coachcap.ErrPreset) && req.Preset != "" {
				return nil, err
			}
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
