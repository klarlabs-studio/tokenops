package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/fmtlearn"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter"
	"go.klarlabs.de/tokenops/internal/infra/fmtindex"
	"go.klarlabs.de/tokenops/internal/infra/jsonlfmt"
)

// mcpJSONLMaxFiles caps how many Claude Code sessions the MCP fmt tools scan,
// so an agent call stays responsive. The CLI can scan everything.
const mcpJSONLMaxFiles = 120

type fmtLearnInput struct {
	RecoverDir string `json:"recover_dir,omitempty" jsonschema:"description=Recovery store dir; defaults to ~/.tokenops/recovery"`
	NoJSONL    bool   `json:"no_jsonl,omitempty" jsonschema:"description=Skip folding in Claude Code log signal (wrapped-run index only)"`
}

type fmtAnalyzeInput struct {
	Root     string `json:"root,omitempty" jsonschema:"description=Claude Code projects dir; defaults to ~/.claude/projects"`
	MaxFiles int    `json:"max_files,omitempty" jsonschema:"description=Cap sessions scanned (newest first); 0 uses the default cap"`
}

// RegisterFmtTools mounts the self-wiring command-output-compression tools on
// s. Both read data that already exists (the recovery index + your Claude
// Code logs) — no daemon, no wrapped commands, no setup:
//
//   - tokenops_fmt_analyze: what fills your context (Read vs Bash vs prose)
//     and what tokenops fmt would save on your real Bash traffic.
//   - tokenops_fmt_learn: advisory report on next formatters to write and
//     over-compression, folding in signal from your Claude Code logs.
//
// Both are read-only and advisory; the formatters stay deterministic and the
// critical-line survival guarantee is untouched.
func RegisterFmtTools(s *Server) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}

	s.Tool("tokenops_fmt_learn").
		Description("Advisory report on where the command-output-compression catalog should improve: next formatters to write (commands falling back to the generic scrub, ranked by bytes), possible over-compression, and per-command loss-level hints. Folds in signal from your Claude Code logs so it reflects real usage with no wrapped commands. Read-only; formatters stay deterministic.").
		Handler(func(_ context.Context, in fmtLearnInput) (string, error) {
			recs, err := fmtindex.Read(in.RecoverDir)
			if err != nil {
				return jsonString(map[string]string{"error": "read_index_failed", "hint": err.Error()}), nil
			}
			if !in.NoJSONL {
				if _, jrecs, err := jsonlfmt.Scan(formatter.DefaultFormatters(), jsonlfmt.Options{MaxFiles: mcpJSONLMaxFiles}, time.Now()); err == nil {
					recs = append(recs, jrecs...)
				}
			}
			rep := fmtlearn.Analyze(recs, fmtlearn.Thresholds{})
			return jsonString(rep), nil
		})

	s.Tool("tokenops_fmt_analyze").
		Description("Mine your Claude Code logs (~/.claude/projects) to show what fills your context — Read (file content) vs Bash (command output) vs prose — and dry-run every Bash command's output through the formatter engine to estimate what tokenops fmt would save on your real traffic. No daemon, no wrapped commands, nothing persisted. Use to see where your input tokens actually go and fmt's ROI on them.").
		Handler(func(_ context.Context, in fmtAnalyzeInput) (string, error) {
			maxFiles := in.MaxFiles
			if maxFiles == 0 {
				maxFiles = mcpJSONLMaxFiles
			}
			rep, _, err := jsonlfmt.Scan(formatter.DefaultFormatters(), jsonlfmt.Options{Root: in.Root, MaxFiles: maxFiles}, time.Now())
			if err != nil {
				return jsonString(map[string]string{"error": "scan_failed", "hint": err.Error()}), nil
			}
			return jsonString(rep), nil
		})

	return nil
}
