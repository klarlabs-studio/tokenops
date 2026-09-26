package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/decide"
	"go.klarlabs.de/tokenops/internal/capability/experiments"
	"go.klarlabs.de/tokenops/internal/capability/learn"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type ExperimentDeps struct{ Manager *experiments.Manager }

type experimentInput struct {
	Action            string                            `json:"action" jsonschema:"description=start | status | stop"`
	ExperimentID      string                            `json:"experiment_id,omitempty"`
	Provider          string                            `json:"provider,omitempty"`
	BaselineModel     string                            `json:"baseline_model,omitempty"`
	VariantModel      string                            `json:"variant_model,omitempty"`
	MaxPairs          int                               `json:"max_pairs,omitempty"`
	DurationDays      int                               `json:"duration_days,omitempty"`
	ObjectiveMetric   string                            `json:"objective_metric,omitempty"`
	MinImprovementPct float64                           `json:"min_improvement_pct,omitempty"`
	Guardrails        []eventschema.ExperimentGuardrail `json:"guardrails,omitempty"`
	Reason            string                            `json:"reason,omitempty"`
}

type experimentResult struct {
	State  *experiments.State `json:"state,omitempty"`
	Belief *learn.Belief      `json:"belief,omitempty"`
	Error  string             `json:"error,omitempty"`
	Hint   string             `json:"hint,omitempty"`
}

func RegisterExperimentTools(s *Server, d ExperimentDeps) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}
	s.Tool("tokenops_experiment").
		Description("Start, inspect, or stop a bounded local model-routing trial. Start requires an explicit lower-is-better objective, minimum improvement percentage, and quality plus measured-resource guardrails. Missing objective or guardrail evidence blocks promotion. Trials only enroll proxy traffic, stay within one provider, randomize baseline/variant order inside matched pairs, and stop after at most 10 pairs or 14 days.").
		OutputSchema(experimentResult{}).
		Handler(func(ctx context.Context, in experimentInput) (*experimentResult, error) {
			if d.Manager == nil {
				return &experimentResult{Error: "storage_disabled", Hint: "run `tokenops init` then restart the daemon"}, nil
			}
			switch strings.ToLower(strings.TrimSpace(in.Action)) {
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
					Guardrails: in.Guardrails,
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
					return nil, fmt.Errorf("experiment_id is required for status")
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
					return nil, fmt.Errorf("experiment_id is required for stop")
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
				return nil, fmt.Errorf("action must be start, status, or stop")
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
