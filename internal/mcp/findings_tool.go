package mcp

import (
	"context"
	"time"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/followthrough"
)

type findingsInput struct{}

// RegisterFindingsTool registers the findings: what the coach and the
// session analysis observed, ranked, with what to do — the answer GET
// /api/findings gives, around the same glance. tokenops_glance serves it
// as its findings view.
func RegisterFindingsTool(s *Server, d PlanDeps) error {
	s.Tool("tokenops_findings").
		Description("What the coach and the session analysis observed, ranked, with what to do.").
		OutputSchema(findings.Report{}).
		Handler(func(ctx context.Context, _ findingsInput) (findings.Report, error) {
			now := time.Now().UTC()
			g, err := headroom.ComputeGlance(ctx, d.headroomDeps(), now)
			if err != nil {
				return findings.Report{}, err
			}
			return findings.Compute(findings.Gather(&g, coachReport(now), findings.DefaultDir())), nil
		})
	return s.Err()
}

// coachReport is the coach's current report, nil when its config cannot be
// read; the findings then leave the coach's own out.
func coachReport(now time.Time) *coachcap.Report {
	path, err := config.DefaultPath()
	if err != nil {
		return nil
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return nil
	}
	var ledger coachcap.Ledger
	if l, err := followthrough.Default(); err == nil {
		ledger = l
	}
	r := coachcap.Status(cfg, ledger, contextLevers(), now)
	return &r
}
