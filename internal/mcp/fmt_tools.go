package mcp

import (
	"context"
	"errors"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/fmtlearn"
	"github.com/felixgeelhaar/tokenops/internal/infra/fmtindex"
)

type fmtLearnInput struct {
	RecoverDir string `json:"recover_dir,omitempty" jsonschema:"description=Recovery store dir; defaults to ~/.tokenops/recovery"`
}

// RegisterFmtTools mounts tokenops_fmt_learn on s. It exposes the offline
// command-output-compression learning loop to agents: which commands lack a
// formatter (falling back to the generic scrub), which formatters look too
// aggressive (compact output re-fetched from the recovery store), and
// per-command loss-level tuning hints. Agents use it to decide where to add
// a config formatter (optimizer.command_fmt.formatters) or adjust a level —
// improving compression over time without recompiling tokenops.
//
// The tool is read-only and advisory: it never changes runtime behaviour
// (the formatters stay deterministic and the critical-line survival
// guarantee is untouched).
func RegisterFmtTools(s *Server) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_fmt_learn").
		Description("Mine `tokenops fmt` telemetry (compression + recovery re-access records) and return an advisory report: next formatters to write (commands falling back to the generic scrub, ranked by raw bytes at stake), possible over-compression (commands whose compact output is re-fetched often), and per-command loss-level tuning hints. Read-only; the formatters stay deterministic.").
		Handler(func(_ context.Context, in fmtLearnInput) (string, error) {
			recs, err := fmtindex.Read(in.RecoverDir)
			if err != nil {
				return jsonString(map[string]string{
					"error": "read_index_failed",
					"hint":  err.Error(),
				}), nil
			}
			rep := fmtlearn.Analyze(recs, fmtlearn.Thresholds{})
			return jsonString(rep), nil
		})
	return nil
}
