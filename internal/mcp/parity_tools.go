package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/auditlog"
	"go.klarlabs.de/tokenops/internal/capability/spending"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// ParityDeps wires the engines the parity tools depend on. Store backs the
// scorecard's live KPIs and the audit log; both answer from the
// capability layer, so there is no adapter-specific logic in this file
// beyond argument unmarshalling. Replay and eval left MCP for the CLI.
type ParityDeps struct {
	Store *sqlite.Store
	Spend *spending.Engine
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

// auditResult is the typed payload for tokenops_records (view=audit), the
// audit-log capability's answer.
type auditResult = auditlog.Log

// RegisterParityTools attaches the scorecard and the audit log, which
// tokenops_records serves as its scorecard and audit views. Read-only.
func RegisterParityTools(s *Server, d ParityDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_scorecard").
		Description("Operator wedge KPI scorecard (FVT, TEU, SAC) computed from the local event store. Mirrors `tokenops scorecard`.").
		OutputSchema(spending.ScorecardReport{}).
		Handler(func(ctx context.Context, in scorecardInput) (*spending.ScorecardReport, error) {
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

func runScorecard(ctx context.Context, d ParityDeps, in scorecardInput) (*spending.ScorecardReport, error) {
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
	limit := in.Limit
	if limit <= 0 {
		limit = defaultAuditLimit
	}
	q := auditlog.Query{Action: in.Action, Actor: in.Actor, Limit: limit}
	if in.Since != "" {
		t, err := parseTimeOrDuration(in.Since)
		if err != nil {
			return nil, inputError(fmt.Errorf("since: %w", err))
		}
		q.Since = t
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return nil, inputError(fmt.Errorf("until: %w", err))
		}
		q.Until = t
	}
	log, err := auditlog.Read(ctx, d.Store, q)
	if err != nil {
		return nil, err
	}
	return &log, nil
}

// defaultAuditLimit bounds an unfiltered audit query: the whole log ran
// past what a client accepts from one tool call.
const defaultAuditLimit = 50
