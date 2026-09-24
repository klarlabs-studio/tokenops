package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/reconstruct"
	"go.klarlabs.de/tokenops/internal/capability/verify"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// VerifyDeps wires the verify tool to the event store and the
// transcripts.
type VerifyDeps struct {
	// Store holds the events consumption is read from. nil disables the
	// tool rather than answering from nothing.
	Store *sqlite.Store
	// Root overrides the transcript root; empty uses the per-client
	// defaults.
	Root string
}

type verifyInput struct {
	Days int `json:"days,omitempty" jsonschema:"description=Window in days (default 30). 0 reads every transcript and event on disk."`
}

// verifyResult is what the agent gets back.
//
// Observational and Proven are separate fields on purpose. An agent
// asking "did compressing that output help?" is the caller most likely
// to take a number at face value and repeat it, so the refusal has to be
// structured rather than buried in prose it may not read.
type verifyResult struct {
	// Reading is the one-line judgement, worded by the capability so
	// this and `tokenops verify` cannot describe it differently.
	Reading string `json:"reading"`
	// DifferenceTokens is the measured difference per attempt. Absent
	// when nothing could be measured — never zero standing in for that.
	DifferenceTokens *float64 `json:"difference_tokens,omitempty"`
	// Observational reports that the cohorts were found rather than
	// made, which is what stops any of this being proof.
	Observational bool `json:"observational"`
	// Proven is false for every observational comparison, whatever the
	// difference looks like.
	Proven bool `json:"proven"`
	// Harmful is the one reading worth acting on immediately.
	Harmful bool `json:"harmful"`
	// Caveat says why the reading is what it is.
	Caveat string `json:"caveat,omitempty"`

	BaselineCount         int                   `json:"baseline_count"`
	InterventionCount     int                   `json:"intervention_count"`
	BaselineOutcomes      verify.OutcomeSummary `json:"baseline_outcomes"`
	InterventionOutcomes  verify.OutcomeSummary `json:"intervention_outcomes"`
	BaselineLatency       any                   `json:"baseline_latency_ms,omitzero"`
	InterventionLatency   any                   `json:"intervention_latency_ms,omitzero"`
	BaselinePlanQuota     verify.QuotaSummary   `json:"baseline_plan_quota_tokens,omitempty"`
	InterventionPlanQuota verify.QuotaSummary   `json:"intervention_plan_quota_tokens,omitempty"`

	Error string `json:"error,omitempty"`
	Hint  string `json:"hint,omitempty"`
}

// verifyPayload renders a report for an agent.
func verifyPayload(r verify.Report) verifyResult {
	out := verifyResult{
		Reading:               r.Reading(),
		Observational:         r.Observational(),
		Harmful:               r.Harmful(),
		Caveat:                r.Verdict.Caveat,
		BaselineCount:         r.BaselineCount,
		InterventionCount:     r.InterventionCount,
		BaselineOutcomes:      r.BaselineOutcomes,
		InterventionOutcomes:  r.InterventionOutcomes,
		BaselineLatency:       r.BaselineLatency,
		InterventionLatency:   r.InterventionLatency,
		BaselinePlanQuota:     r.BaselinePlanQuota,
		InterventionPlanQuota: r.InterventionPlanQuota,
	}
	// Proven requires both an assigned comparison and a conclusion. An
	// observational split can never reach it.
	out.Proven = !r.Observational() && r.Verdict.Conclusive()
	if amount, ok := r.Verdict.Observed.Amount(); ok {
		out.DifferenceTokens = &amount
	}
	return out
}

// RegisterVerifyTool mounts tokenops_verify.
//
// It answers the question TokenOps could never answer: not "how many
// tokens did the optimizer remove", which it has always reported, but
// "did removing them make anything better". The honest answer today is
// that the comparison exists and its cause does not, and saying so is
// the point rather than a limitation to be papered over.
func RegisterVerifyTool(s *Server, d VerifyDeps) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}
	s.Tool("tokenops_verify").
		Description("Compare the attempts an optimization touched against the ones it did not, over real recorded work. Returns measured token difference, mean proxy request latency, explicit outcome success rates, and plan-included quota tokens separately by provider. Quota tokens are not dollar cost. Outcome drops can flag harm, but the cohorts remain observational unless assignments were randomized; read `observational` and `proven` before attributing a difference to the optimization.").
		OutputSchema(verifyResult{}).
		Handler(func(ctx context.Context, in verifyInput) (*verifyResult, error) {
			if d.Store == nil {
				return &verifyResult{
					Error: "storage_disabled",
					Hint:  "run `tokenops init` then restart the daemon",
				}, nil
			}
			days := in.Days
			if days == 0 {
				days = 30
			}

			extract := agentdx.ExtractOptions{Root: d.Root, WithPromptText: true}
			if days > 0 {
				extract.Since = time.Now().AddDate(0, 0, -days)
			}
			// A reader that broke is not fatal: whatever the other
			// clients yielded is still worth comparing, and failing the
			// call would hide the comparison entirely.
			records, _ := agentdx.ExtractAll(extract)

			filter := sqlite.Filter{Limit: 200_000}
			if days > 0 {
				filter.Since = time.Now().AddDate(0, 0, -days)
			}
			events, err := d.Store.Query(ctx, filter)
			if err != nil {
				return nil, err
			}

			report := verify.CompareReconstructed(
				reconstruct.FromUnits(agentdx.Units(records), reconstruct.Options{}),
				events,
			)
			res := verifyPayload(report)
			return &res, nil
		})
	return nil
}
