package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/governance/coverdebt"
	"github.com/felixgeelhaar/tokenops/internal/contexts/governance/scorecard"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/eval"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/replay"
	"github.com/felixgeelhaar/tokenops/internal/contexts/rules"
	"github.com/felixgeelhaar/tokenops/internal/contexts/security/audit"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/internal/infra/rulesfs"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
)

// ParityDeps wires the engines the parity tools depend on. Store is reused
// for replay + scorecard live KPI computation. CLI and MCP adapters call
// the same domain service functions (rules.RunBenchSpec, eval.Run,
// scorecard.BuildFromStore, coverdebt.Analyze, replay.Engine) — there is
// no adapter-specific logic in this file beyond argument unmarshalling.
type ParityDeps struct {
	Store *sqlite.Store
	Spend *spend.Engine
}

// --- input structs --------------------------------------------------------

type rulesBenchInput struct {
	SpecJSON string `json:"spec_json,omitempty"`
	SpecPath string `json:"spec_path,omitempty"`
}

type evalInput struct {
	Suites         string   `json:"suites,omitempty"`
	Baseline       string   `json:"baseline,omitempty"`
	MaxSuccessDrop float64  `json:"max_success_drop_pct,omitempty"`
	MaxQualityDrop float64  `json:"max_quality_drift_pct,omitempty"`
	MinCases       int      `json:"min_cases,omitempty"`
	Optimizers     []string `json:"optimizers,omitempty"`
}

type coverageDebtInput struct {
	Profile string `json:"profile,omitempty" jsonschema:"description=path to coverage.out (default 'coverage.out')"`
}

type scorecardInput struct {
	SinceDays   int     `json:"since_days,omitempty"`
	FVTSeconds  float64 `json:"fvt_seconds,omitempty"`
	TEUPct      float64 `json:"teu_pct,omitempty"`
	SACPct      float64 `json:"sac_pct,omitempty"`
	BaselineRef string  `json:"baseline_ref,omitempty"`
}

type replayInput struct {
	SessionID  string `json:"session_id,omitempty"`
	WorkflowID string `json:"workflow_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	Since      string `json:"since,omitempty"`
	Until      string `json:"until,omitempty"`
}

type auditInput struct {
	Action string `json:"action,omitempty"`
	Actor  string `json:"actor,omitempty"`
	Since  string `json:"since,omitempty"`
	Until  string `json:"until,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// RegisterParityTools attaches MCP tools that mirror the CLI surface
// (rules bench, eval, coverage-debt, scorecard, replay, audit). Read-only.
func RegisterParityTools(s *Server, d ParityDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_rules_bench").
		Description("Benchmark rule profiles against scenarios. Mirrors `tokenops rules bench --spec`. Accepts the same YAML/JSON spec inline via 'spec_json' or a path via 'spec_path'.").
		Handler(func(_ context.Context, in rulesBenchInput) (string, error) {
			return rulesBench(in)
		})

	s.Tool("tokenops_eval").
		Description("Run the optimizer eval harness. Mirrors `tokenops eval`. Returns the merged report and gate result.").
		Handler(runEval)

	s.Tool("tokenops_coverage_debt").
		Description("Risk-ranked coverage debt report from a Go cover profile. Mirrors `tokenops coverage-debt`.").
		Handler(func(_ context.Context, in coverageDebtInput) (string, error) {
			return runCoverageDebt(in)
		})

	s.Tool("tokenops_scorecard").
		Description("Operator wedge KPI scorecard (FVT, TEU, SAC) computed from the local event store. Mirrors `tokenops scorecard`.").
		Handler(func(ctx context.Context, in scorecardInput) (string, error) {
			return runScorecard(ctx, d, in)
		})

	if d.Store != nil && d.Spend != nil {
		s.Tool("tokenops_replay").
			Description("Replay a session/workflow through the optimizer pipeline. Mirrors `tokenops replay`. One of session_id / workflow_id / agent_id is required.").
			Handler(func(ctx context.Context, in replayInput) (string, error) {
				return runReplay(ctx, d, in)
			})
	}
	if d.Store != nil {
		s.Tool("tokenops_audit").
			Description("Query the audit log. Filter by action, actor, since (RFC3339 or Nd|24h), until (RFC3339), limit. Returns entries newest-first.").
			Handler(func(ctx context.Context, in auditInput) (string, error) {
				return runAudit(ctx, d, in)
			})
	}
	return nil
}

// --- handlers -------------------------------------------------------------

func rulesBench(in rulesBenchInput) (string, error) {
	var data []byte
	switch {
	case in.SpecJSON != "":
		data = []byte(in.SpecJSON)
	case in.SpecPath != "":
		b, err := os.ReadFile(in.SpecPath)
		if err != nil {
			return "", fmt.Errorf("read spec: %w", err)
		}
		data = b
	default:
		return "", errors.New("provide spec_json or spec_path")
	}
	spec, err := rules.ParseBenchSpec(data)
	if err != nil {
		return "", err
	}
	res, err := rules.RunBenchSpec(spec, rulesfs.LoadCorpus)
	if err != nil {
		return "", err
	}
	return jsonString(res), nil
}

func runEval(ctx context.Context, in evalInput) (string, error) {
	typed := make([]eval.OptimizationType, 0, len(in.Optimizers))
	for _, o := range in.Optimizers {
		typed = append(typed, eval.OptimizationType(o))
	}
	result, err := eval.Run(ctx, eval.RunParams{
		Suites:           in.Suites,
		BaselinePath:     in.Baseline,
		OptimizerFilters: typed,
		Gate: eval.Gate{
			MaxSuccessRateDropPct: in.MaxSuccessDrop,
			MaxQualityDriftPct:    in.MaxQualityDrop,
			MinTotalCases:         in.MinCases,
		},
	})
	if err != nil {
		return "", err
	}
	return jsonString(struct {
		Report *eval.Report     `json:"report"`
		Gate   *eval.GateResult `json:"gate"`
	}{Report: result.Report, Gate: result.Gate}), nil
}

func runCoverageDebt(in coverageDebtInput) (string, error) {
	profile := in.Profile
	if profile == "" {
		profile = "coverage.out"
	}
	cov, err := coverdebt.ReadProfile(profile)
	if err != nil {
		return "", fmt.Errorf("read profile: %w", err)
	}
	return jsonString(coverdebt.Analyze(cov, coverdebt.DefaultPolicies)), nil
}

func runScorecard(ctx context.Context, d ParityDeps, in scorecardInput) (string, error) {
	s := scorecard.BuildFromStore(ctx, d.Store, scorecard.BuildParams{
		SinceDays:          in.SinceDays,
		FVTSecondsOverride: in.FVTSeconds,
		TEUPctOverride:     in.TEUPct,
		SACPctOverride:     in.SACPct,
		BaselineRef:        in.BaselineRef,
	})
	return jsonString(s), nil
}

func runReplay(ctx context.Context, d ParityDeps, in replayInput) (string, error) {
	if in.SessionID == "" && in.WorkflowID == "" && in.AgentID == "" {
		return "", errors.New("provide session_id, workflow_id, or agent_id")
	}
	sel := replay.SessionSelector{
		SessionID:  in.SessionID,
		WorkflowID: in.WorkflowID,
		AgentID:    in.AgentID,
		Limit:      in.Limit,
	}
	if in.Since != "" {
		t, err := parseTimeOrDuration(in.Since)
		if err != nil {
			return "", fmt.Errorf("since: %w", err)
		}
		sel.Since = t
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return "", fmt.Errorf("until: %w", err)
		}
		sel.Until = t
	}
	eng := replay.New(d.Store, replay.DefaultPipeline(nil), d.Spend)
	res, err := eng.Replay(ctx, sel)
	if err != nil {
		return "", err
	}
	return jsonString(res), nil
}

func runAudit(ctx context.Context, d ParityDeps, in auditInput) (string, error) {
	rec := audit.NewRecorder(d.Store)
	f := audit.Filter{
		Action: audit.Action(in.Action),
		Actor:  in.Actor,
		Limit:  in.Limit,
	}
	if in.Since != "" {
		t, err := parseTimeOrDuration(in.Since)
		if err != nil {
			return "", fmt.Errorf("since: %w", err)
		}
		f.Since = t
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return "", fmt.Errorf("until: %w", err)
		}
		f.Until = t
	}
	entries, err := rec.Query(ctx, f)
	if err != nil {
		return "", err
	}
	return jsonString(struct {
		Entries []audit.Entry `json:"entries"`
	}{Entries: entries}), nil
}
