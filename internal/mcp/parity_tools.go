package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/spending"

	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/eval"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
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
	// Pipeline overrides the optimizer pipeline used by `tokenops replay`.
	// nil falls back to replay.DefaultPipeline. The serve adapter passes
	// a pipeline built from config (optimizer.routing_rules etc.) so MCP
	// replays match `tokenops replay`.
	Pipeline *optimizer.Pipeline
	// PipelineFor, when set, builds the pipeline at call time from the
	// live config, so a routing rule written mid-session shows in the
	// next replay. It wins over Pipeline.
	PipelineFor func() *optimizer.Pipeline
}

// --- input structs --------------------------------------------------------

type scorecardInput struct {
	SinceDays   int     `json:"since_days,omitempty"`
	FVTSeconds  float64 `json:"fvt_seconds,omitempty"`
	TEUPct      float64 `json:"teu_pct,omitempty"`
	SACPct      float64 `json:"sac_pct,omitempty"`
	BaselineRef string  `json:"baseline_ref,omitempty"`
}

type auditInput struct {
	Action string `json:"action,omitempty"`
	Actor  string `json:"actor,omitempty"`
	Since  string `json:"since,omitempty"`
	Until  string `json:"until,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// --- output structs --------------------------------------------------------

// evalResult is the typed payload for `tokenops eval`.
type evalResult struct {
	Report *eval.Report     `json:"report"`
	Gate   *eval.GateResult `json:"gate"`
}

// auditResult is the typed payload for tokenops_records (view=audit).
type auditResult struct {
	Entries []audit.Entry `json:"entries"`
}

// RegisterParityTools attaches the scorecard and the audit log, which
// tokenops_records serves as its scorecard and audit views. Read-only.
func RegisterParityTools(s *Server, d ParityDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_scorecard").
		Description("Operator wedge KPI scorecard (FVT, TEU, SAC) computed from the local event store. Mirrors `tokenops scorecard`.").
		OutputSchema(scorecard.Scorecard{}).
		Handler(func(ctx context.Context, in scorecardInput) (*scorecard.Scorecard, error) {
			return runScorecard(ctx, d, in)
		})

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

func runScorecard(ctx context.Context, d ParityDeps, in scorecardInput) (*scorecard.Scorecard, error) {
	s := spending.Scorecard(ctx, d.Store, spending.ScorecardParams{
		SinceDays:   in.SinceDays,
		FVTSeconds:  in.FVTSeconds,
		TEUPct:      in.TEUPct,
		SACPct:      in.SACPct,
		BaselineRef: in.BaselineRef,
	})
	return s, nil
}

func runAudit(ctx context.Context, d ParityDeps, in auditInput) (*auditResult, error) {
	rec := audit.NewRecorder(d.Store)
	limit := in.Limit
	if limit <= 0 {
		limit = defaultAuditLimit
	}
	f := audit.Filter{
		Action: audit.Action(in.Action),
		Actor:  in.Actor,
		Limit:  limit,
	}
	if in.Since != "" {
		t, err := parseTimeOrDuration(in.Since)
		if err != nil {
			return nil, inputError(fmt.Errorf("since: %w", err))
		}
		f.Since = t
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return nil, inputError(fmt.Errorf("until: %w", err))
		}
		f.Until = t
	}
	entries, err := rec.Query(ctx, f)
	if err != nil {
		return nil, err
	}
	return &auditResult{Entries: entries}, nil
}

// pipeline is the replay pipeline in effect for this call; nil means the
// caller falls back to the default.
func (d ParityDeps) pipeline() *optimizer.Pipeline {
	if d.PipelineFor != nil {
		return d.PipelineFor()
	}
	return d.Pipeline
}

// defaultAuditLimit bounds an unfiltered audit query: the whole log ran
// past what a client accepts from one tool call.
const defaultAuditLimit = 50
