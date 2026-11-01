package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/fmtinsight"
	"go.klarlabs.de/tokenops/internal/config"
)

// mcpJSONLMaxFiles caps how many Claude Code sessions the MCP fmt tools scan,
// so an agent call stays responsive. The CLI can scan everything.
const mcpJSONLMaxFiles = 120

type fmtLearnInput struct {
	Limit      int    `json:"limit,omitempty" jsonschema:"description=Most commands per list (default 20); totals are always given"`
	RecoverDir string `json:"recover_dir,omitempty" jsonschema:"description=Recovery store dir; defaults to ~/.tokenops/recovery"`
	NoJSONL    bool   `json:"no_jsonl,omitempty" jsonschema:"description=Skip folding in Claude Code log signal (wrapped-run index only)"`
}

type fmtExperimentInput struct {
	Days       int    `json:"days,omitempty" jsonschema:"description=Window in days (default 30)"`
	RecoverDir string `json:"recover_dir,omitempty" jsonschema:"description=Recovery store dir; defaults to ~/.tokenops/recovery"`
}

type fmtAnalyzeInput struct {
	Root     string `json:"root,omitempty" jsonschema:"description=Claude Code projects dir; defaults to ~/.claude/projects"`
	MaxFiles int    `json:"max_files,omitempty" jsonschema:"description=Cap sessions scanned (newest first); 0 uses the default cap"`
}

// RegisterFmtTools mounts the self-wiring command-output-compression tools on
// s. Both read data that already exists (the recovery index + your Claude
// Code logs) — no daemon, no wrapped commands, no setup:
//
//   - tokenops_fmt (view=analyze): what fills your context (Read vs Bash vs prose)
//     and what tokenops fmt would save on your real Bash traffic.
//   - tokenops_fmt (view=learn): advisory report on next formatters to write and
//     over-compression, folding in signal from your Claude Code logs.
//
// Both are read-only and advisory; the formatters stay deterministic and the
// critical-line survival guarantee is untouched.
func RegisterFmtTools(s *Server, d FmtDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}

	s.Tool("tokenops_fmt_learn").
		Description("Advisory report on where the command-output-compression catalog should improve: next formatters to write (commands falling back to the generic scrub, ranked by bytes), possible over-compression, and per-command loss-level hints. Folds in signal from your Claude Code logs so it reflects real usage with no wrapped commands. Read-only; formatters stay deterministic.").
		Handler(func(_ context.Context, in fmtLearnInput) (string, error) {
			o := fmtinsight.LearnOptions{RecoverDir: in.RecoverDir}
			if !in.NoJSONL {
				o.Sessions = &fmtinsight.Window{MaxFiles: mcpJSONLMaxFiles}
			}
			rep, err := fmtinsight.Learn(d.commandFmt(), o, time.Now())
			if err != nil {
				return jsonString(map[string]string{"error": "read_index_failed", "hint": err.Error()}), nil
			}
			return jsonString(trimLearnReport(rep, in.Limit)), nil
		})

	s.Tool("tokenops_fmt_analyze").
		Description("Mine your Claude Code logs (~/.claude/projects) to show what fills your context — Read (file content) vs Bash (command output) vs prose — and dry-run every Bash command's output through the formatter engine to estimate what tokenops fmt would save on your real traffic. No daemon, no wrapped commands, nothing persisted. Use to see where your input tokens actually go and fmt's ROI on them.").
		Handler(func(_ context.Context, in fmtAnalyzeInput) (string, error) {
			maxFiles := in.MaxFiles
			if maxFiles == 0 {
				maxFiles = mcpJSONLMaxFiles
			}
			rep, err := fmtinsight.Analyze(d.commandFmt(), fmtinsight.Window{Root: in.Root, MaxFiles: maxFiles}, time.Now())
			if err != nil {
				return jsonString(map[string]string{"error": "scan_failed", "hint": err.Error()}), nil
			}
			return jsonString(rep), nil
		})

	s.Tool("tokenops_fmt_experiment").
		Description("The fmt outcome experiment: with it on, a share of sessions drawn at random by session id get raw command output, and the arms are compared on outcomes rather than bytes saved: first-try and reworked instructions, repeated tool calls, tokens per instruction, and outcomes recorded under the session id. Counts per arm, the difference with a rough 95% interval over sessions, and \"not enough sessions yet\" until each arm has 4 sessions and 20 instructions. A difference that excludes zero is a lead to check, not a finding. Read-only; tokenops_experiment action fmt turns it on or off.").
		Handler(func(ctx context.Context, in fmtExperimentInput) (string, error) {
			days := in.Days
			if days <= 0 {
				days = fmtinsight.DefaultExperimentDays
			}
			since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
			var outcomes map[string]string
			if d.Outcomes != nil {
				outcomes = d.Outcomes(ctx, since)
			}
			rep, err := fmtinsight.Experiment(d.commandFmt().Experiment, fmtinsight.ExperimentOptions{
				RecoverDir: in.RecoverDir, Since: since, Outcomes: outcomes,
			})
			if err != nil {
				return jsonString(map[string]string{"error": "read_index_failed", "hint": err.Error()}), nil
			}
			return jsonString(rep), nil
		})

	return nil
}

// learnReport is the fmt learn report with its lists cut to the top
// entries, and how many each had.
type learnReport struct {
	fmtinsight.LearnReport
	CommandsTotal       int `json:"commands_total"`
	NextFormattersTotal int `json:"next_formatters_total"`
	LevelHintsTotal     int `json:"level_hints_total"`
}

// defaultLearnLimit keeps the report readable by an agent: the full one
// ran to 190 KB, past what a client accepts from one tool call.
const defaultLearnLimit = 20

func trimLearnReport(r fmtinsight.LearnReport, limit int) learnReport {
	if limit <= 0 {
		limit = defaultLearnLimit
	}
	out := learnReport{LearnReport: r, CommandsTotal: len(r.Commands), NextFormattersTotal: len(r.NextFormatters), LevelHintsTotal: len(r.LevelHints)}
	out.Commands = r.Commands[:min(limit, len(r.Commands))]
	out.NextFormatters = r.NextFormatters[:min(limit, len(r.NextFormatters))]
	out.LevelHints = r.LevelHints[:min(limit, len(r.LevelHints))]
	return out
}

// FmtDeps wires the fmt tools to the operator's configuration, whose
// formatters extend the built-in catalog.
type FmtDeps struct {
	// ConfigGetter returns the live configuration; nil reads the
	// built-in catalog alone.
	ConfigGetter func() *config.Config
	// Outcomes reads the recorded outcomes since a time, by execution
	// id; nil compares the experiment's arms on transcripts alone.
	Outcomes func(ctx context.Context, since time.Time) map[string]string
}

// commandFmt is the fmt configuration in effect now.
func (d FmtDeps) commandFmt() config.CommandFmtConfig {
	if d.ConfigGetter == nil {
		return config.CommandFmtConfig{}
	}
	if cfg := d.ConfigGetter(); cfg != nil {
		return cfg.Optimizer.CommandFmt
	}
	return config.CommandFmtConfig{}
}
