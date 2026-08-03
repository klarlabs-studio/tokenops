package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/coverdebt"
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/eval"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/replay"
	"go.klarlabs.de/tokenops/internal/contexts/rules"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/infra/rulesfs"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// ParityDeps wires the engines the parity tools depend on. Store is reused
// for replay + scorecard live KPI computation. CLI and MCP adapters call
// the same domain service functions (rules.RunBenchSpec, eval.Run,
// scorecard.BuildFromStore, coverdebt.Analyze, replay.Engine) — there is
// no adapter-specific logic in this file beyond argument unmarshalling.
type ParityDeps struct {
	Store *sqlite.Store
	Spend *spend.Engine
	// Pipeline overrides the optimizer pipeline used by tokenops_replay.
	// nil falls back to replay.DefaultPipeline. The serve adapter passes
	// a pipeline built from config (optimizer.routing_rules etc.) so MCP
	// replays match `tokenops replay`.
	Pipeline *optimizer.Pipeline
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

// --- output structs --------------------------------------------------------

// evalResult is the typed payload for tokenops_eval.
type evalResult struct {
	Report *eval.Report     `json:"report"`
	Gate   *eval.GateResult `json:"gate"`
}

// auditResult is the typed payload for tokenops_audit.
type auditResult struct {
	Entries []audit.Entry `json:"entries"`
}

// RegisterParityTools attaches MCP tools that mirror the CLI surface
// (rules bench, eval, coverage-debt, scorecard, replay, audit). Read-only.
func RegisterParityTools(s *Server, d ParityDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_rules_bench").
		Description("Benchmark rule profiles against scenarios. Mirrors `tokenops rules bench --spec`. Accepts the same YAML/JSON spec inline via 'spec_json' or a path via 'spec_path'.").
		OutputSchema(rules.BenchmarkResult{}).
		Handler(func(_ context.Context, in rulesBenchInput) (*rules.BenchmarkResult, error) {
			return rulesBench(in)
		})

	s.Tool("tokenops_eval").
		Description("Run the optimizer eval harness. Mirrors `tokenops eval`. Returns the merged report and gate result.").
		OutputSchema(evalResult{}).
		Handler(runEval)

	s.Tool("tokenops_coverage_debt").
		Description("Risk-ranked coverage debt report from a Go cover profile. Mirrors `tokenops coverage-debt`.").
		OutputSchema(coverdebt.Report{}).
		Handler(func(_ context.Context, in coverageDebtInput) (*coverdebt.Report, error) {
			return runCoverageDebt(in)
		})

	s.Tool("tokenops_scorecard").
		Description("Operator wedge KPI scorecard (FVT, TEU, SAC) computed from the local event store. Mirrors `tokenops scorecard`.").
		OutputSchema(scorecard.Scorecard{}).
		Handler(func(ctx context.Context, in scorecardInput) (*scorecard.Scorecard, error) {
			return runScorecard(ctx, d, in)
		})

	if d.Store != nil && d.Spend != nil {
		s.Tool("tokenops_replay").
			Description("Replay a session/workflow through the optimizer pipeline. Mirrors `tokenops replay`. One of session_id / workflow_id / agent_id is required.").
			OutputSchema(replay.Result{}).
			Handler(func(ctx context.Context, in replayInput) (*replay.Result, error) {
				return runReplay(ctx, d, in)
			})
	}
	if d.Store != nil {
		s.Tool("tokenops_audit").
			Description("Query the audit log. Filter by action, actor, since (RFC3339 or Nd|24h), until (RFC3339), limit. Returns entries newest-first.").
			OutputSchema(auditResult{}).
			Handler(func(ctx context.Context, in auditInput) (*auditResult, error) {
				return runAudit(ctx, d, in)
			})
	}
	return nil
}

// --- handlers -------------------------------------------------------------

func rulesBench(in rulesBenchInput) (*rules.BenchmarkResult, error) {
	var data []byte
	switch {
	case in.SpecJSON != "":
		data = []byte(in.SpecJSON)
	case in.SpecPath != "":
		b, err := os.ReadFile(in.SpecPath)
		if err != nil {
			return nil, fmt.Errorf("read spec: %w", err)
		}
		data = b
	default:
		return nil, errors.New("provide spec_json or spec_path")
	}
	spec, err := rules.ParseBenchSpec(data)
	if err != nil {
		return nil, err
	}
	res, err := rules.RunBenchSpec(spec, rulesfs.LoadCorpus)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func runEval(ctx context.Context, in evalInput) (evalResult, error) {
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
		return evalResult{}, err
	}
	return evalResult{Report: result.Report, Gate: result.Gate}, nil
}

func runCoverageDebt(in coverageDebtInput) (*coverdebt.Report, error) {
	profile := in.Profile
	if profile == "" {
		profile = "coverage.out"
	}
	cov, err := coverdebt.ReadProfile(profile)
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	return coverdebt.Analyze(cov, coverdebt.DefaultPolicies), nil
}

func runScorecard(ctx context.Context, d ParityDeps, in scorecardInput) (*scorecard.Scorecard, error) {
	s := scorecard.BuildFromStore(ctx, d.Store, scorecard.BuildParams{
		SinceDays:          in.SinceDays,
		FVTSecondsOverride: in.FVTSeconds,
		TEUPctOverride:     in.TEUPct,
		SACPctOverride:     in.SACPct,
		BaselineRef:        in.BaselineRef,
	})
	return s, nil
}

func runReplay(ctx context.Context, d ParityDeps, in replayInput) (*replay.Result, error) {
	if in.SessionID == "" && in.WorkflowID == "" && in.AgentID == "" {
		return nil, errors.New("provide session_id, workflow_id, or agent_id")
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
			return nil, fmt.Errorf("since: %w", err)
		}
		sel.Since = t
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return nil, fmt.Errorf("until: %w", err)
		}
		sel.Until = t
	}
	pipeline := d.Pipeline
	if pipeline == nil {
		pipeline = replay.DefaultPipeline(nil)
	}
	eng := replay.New(d.Store, pipeline, d.Spend)
	res, err := eng.Replay(ctx, sel)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func runAudit(ctx context.Context, d ParityDeps, in auditInput) (*auditResult, error) {
	rec := audit.NewRecorder(d.Store)
	f := audit.Filter{
		Action: audit.Action(in.Action),
		Actor:  in.Actor,
		Limit:  in.Limit,
	}
	if in.Since != "" {
		t, err := parseTimeOrDuration(in.Since)
		if err != nil {
			return nil, fmt.Errorf("since: %w", err)
		}
		f.Since = t
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return nil, fmt.Errorf("until: %w", err)
		}
		f.Until = t
	}
	entries, err := rec.Query(ctx, f)
	if err != nil {
		return nil, err
	}
	return &auditResult{Entries: entries}, nil
}
