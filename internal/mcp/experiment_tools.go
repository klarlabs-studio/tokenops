package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/decide"
	"go.klarlabs.de/tokenops/internal/capability/experiments"
	"go.klarlabs.de/tokenops/internal/capability/fmtinsight"
	"go.klarlabs.de/tokenops/internal/capability/learn"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// ExperimentDeps wires the experiment tool: the routing trials' manager,
// and the config file the fmt experiment's share is written to (empty is
// the default path).
type ExperimentDeps struct {
	Manager    *experiments.Manager
	ConfigPath string
}

type experimentInput struct {
	Action            string                            `json:"action" jsonschema:"description=start | status | stop for a routing trial; fmt to set the fmt outcome experiment"`
	ExperimentID      string                            `json:"experiment_id,omitempty" jsonschema:"description=status and stop: the experiment, as start returned it"`
	Provider          string                            `json:"provider,omitempty" jsonschema:"description=start: the provider whose traffic is split, e.g. anthropic"`
	BaselineModel     string                            `json:"baseline_model,omitempty" jsonschema:"description=start: the model requests run on today"`
	VariantModel      string                            `json:"variant_model,omitempty" jsonschema:"description=start: the model the trial compares against it"`
	MaxPairs          int                               `json:"max_pairs,omitempty" jsonschema:"description=start: most paired requests before the trial ends"`
	DurationDays      int                               `json:"duration_days,omitempty" jsonschema:"description=start: days the trial runs, at most 14"`
	ObjectiveMetric   string                            `json:"objective_metric,omitempty" jsonschema:"enum=tokens,enum=plan_quota_tokens,enum=metered_cost_usd,enum=latency_ms,enum=attention_minutes,description=start: what the variant must improve"`
	MinImprovementPct float64                           `json:"min_improvement_pct,omitempty" jsonschema:"description=start: improvement the variant must show to win, in percent (above 0, at most 100)"`
	Guardrails        []eventschema.ExperimentGuardrail `json:"guardrails,omitempty" jsonschema:"description=start: at least one metric with the most regression it may show; quality must use 0"`
	Reason            string                            `json:"reason,omitempty" jsonschema:"description=start and stop: why, for the record"`
	FmtShare          string                            `json:"fmt_share,omitempty" jsonschema:"description=fmt: share of sessions whose command output fmt passes through raw, so fmt is compared on outcomes: off | on (50%) | a percent 0-50. Only on the operator's word."`
}

// fmtExperimentState is the fmt experiment's setting after a change.
type fmtExperimentState struct {
	RawPercent int    `json:"raw_percent"`
	Note       string `json:"note"`
}

type experimentResult struct {
	State  *experiments.State  `json:"state,omitempty"`
	Belief *learn.Belief       `json:"belief,omitempty"`
	Fmt    *fmtExperimentState `json:"fmt,omitempty"`
	Error  string              `json:"error,omitempty"`
	Hint   string              `json:"hint,omitempty"`
}

func RegisterExperimentTools(s *Server, d ExperimentDeps) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}
	s.Tool("tokenops_experiment").
		Description("Turn the fmt outcome experiment on or off (action fmt with fmt_share: that share of sessions, drawn by session id, get raw command output, and tokenops_fmt view experiment compares the arms on outcomes). Or start, inspect, or stop a bounded local model-routing trial. Start requires an explicit lower-is-better objective, minimum improvement percentage, and quality plus measured-resource guardrails. Missing objective or guardrail evidence blocks promotion. Trials only enroll proxy traffic, stay within one provider, randomize baseline/variant order inside matched pairs, and stop after at most 10 pairs or 14 days.").
		OutputSchema(experimentResult{}).
		Handler(func(ctx context.Context, in experimentInput) (*experimentResult, error) {
			action := strings.ToLower(strings.TrimSpace(in.Action))
			if action == "fmt" {
				return setFmtExperiment(d.ConfigPath, in.FmtShare)
			}
			if d.Manager == nil {
				return &experimentResult{Error: "storage_disabled", Hint: "run `tokenops init` then restart the daemon"}, nil
			}
			switch action {
			case "start":
				days := in.DurationDays
				if days == 0 {
					days = 14
				}
				state, err := d.Manager.Start(ctx, experiments.StartInput{
					Provider: in.Provider, BaselineModel: in.BaselineModel, VariantModel: in.VariantModel,
					Fingerprint: decide.RouteFingerprint(eventschema.Provider(in.Provider), in.BaselineModel, in.VariantModel, "proxy"),
					MaxPairs:    in.MaxPairs, Duration: time.Duration(days) * 24 * time.Hour,
					ObjectiveMetric: in.ObjectiveMetric, MinImprovementPct: in.MinImprovementPct,
					Guardrails: in.Guardrails, Reason: strings.TrimSpace(in.Reason),
				})
				if err != nil {
					return nil, err
				}
				belief, err := experimentBelief(ctx, d.Manager, state)
				if err != nil {
					return nil, err
				}
				return &experimentResult{State: &state, Belief: &belief}, nil
			case "status":
				if strings.TrimSpace(in.ExperimentID) == "" {
					return nil, inputError(errors.New("experiment_id is required for status"))
				}
				state, ok, err := d.Manager.Status(ctx, in.ExperimentID, time.Time{})
				if err != nil {
					return nil, err
				}
				if !ok {
					return &experimentResult{Error: "experiment_not_found"}, nil
				}
				belief, err := experimentBelief(ctx, d.Manager, state)
				if err != nil {
					return nil, err
				}
				return &experimentResult{State: &state, Belief: &belief}, nil
			case "stop":
				if strings.TrimSpace(in.ExperimentID) == "" {
					return nil, inputError(errors.New("experiment_id is required for stop"))
				}
				if err := d.Manager.Stop(ctx, in.ExperimentID, strings.TrimSpace(in.Reason), time.Time{}); err != nil {
					return nil, err
				}
				state, _, err := d.Manager.Status(ctx, in.ExperimentID, time.Time{})
				if err != nil {
					return nil, err
				}
				return &experimentResult{State: &state}, nil
			default:
				return nil, inputError(errors.New("action must be start, status, stop, or fmt"))
			}
		})
	return nil
}

func experimentBelief(ctx context.Context, manager *experiments.Manager, state experiments.State) (learn.Belief, error) {
	history, err := manager.Events(ctx, state.ID)
	if err != nil {
		return learn.Belief{}, err
	}
	return learn.Routing(history, state.Fingerprint, time.Now().UTC()), nil
}

// setFmtExperiment writes the fmt experiment's share to the config. fmt
// reads the config on every run, so it takes effect with the next command.
func setFmtExperiment(path, share string) (*experimentResult, error) {
	if strings.TrimSpace(share) == "" {
		return nil, inputError(errors.New("fmt_share is required for fmt: off, on, or a percent from 0 to 50"))
	}
	n, err := fmtinsight.ParseExperiment(share)
	if err != nil {
		return nil, inputError(err)
	}
	if path == "" {
		if path, err = config.DefaultPath(); err != nil {
			return nil, inputError(err)
		}
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return nil, inputError(err)
	}
	cfg.Optimizer.CommandFmt.Experiment = n
	if err := config.WriteMutable(path, cfg); err != nil {
		return nil, err
	}
	note := "off: fmt compresses every session"
	if n > 0 {
		note = fmt.Sprintf("on: %d%% of sessions, drawn by session id, get raw command output from their next command; tokenops_fmt view experiment compares the arms", n)
	}
	return &experimentResult{Fmt: &fmtExperimentState{RawPercent: n, Note: note}}, nil
}
