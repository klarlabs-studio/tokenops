package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/fmtlearn"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/formatter"
	"github.com/felixgeelhaar/tokenops/internal/infra/fmtindex"
)

// appendLearnRecord and readLearnRecords delegate to the shared fmtindex
// adapter so the CLI and the MCP tool surface read/write one index.
func appendLearnRecord(recoverDir string, rec fmtlearn.Record) error {
	return fmtindex.Append(recoverDir, rec)
}

func readLearnRecords(recoverDir string) ([]fmtlearn.Record, error) {
	return fmtindex.Read(recoverDir)
}

// recordCompressRun appends a compress record for a completed fmt run. Called
// from the fmt RunE after the wrapped command finishes. now is injected so
// the caller controls the timestamp (and tests stay deterministic).
func recordCompressRun(recoverDir, command, level string, res *fmtResult, now time.Time) error {
	if res.RecoveryID == "" {
		return nil // recovery disabled -> nothing to correlate against
	}
	return appendLearnRecord(recoverDir, fmtlearn.Record{
		Type:            fmtlearn.RecordCompress,
		ID:              res.RecoveryID,
		Command:         command,
		Level:           level,
		RawBytes:        int64(res.BytesBefore),
		CompactBytes:    int64(res.BytesAfter),
		TokensSaved:     int64(estTokens(res.BytesBefore - res.BytesAfter)),
		Handled:         res.Handled,
		GenericFallback: !res.Handled,
		CriticalKept:    res.CriticalKept,
		TS:              now.UTC(),
	})
}

// newFmtRecoverCmd prints the full stored output for a recovery id and
// records the access — the signal that the compact view was insufficient,
// which the learning loop reads as a possible critical-line miss.
func newFmtRecoverCmd() *cobra.Command {
	var recoverDir string
	cmd := &cobra.Command{
		Use:   "recover <id>",
		Short: "Print the full stored output for a recovery id (records the re-access)",
		Long: `recover prints the complete output a prior 'tokenops fmt' run
stored, and logs the access so 'tokenops fmt learn' can detect commands
whose compression dropped something the agent needed.

  tokenops fmt recover 20260702T190630-b5fbee4ef550`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			dir := recoverDir
			if dir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				dir = filepath.Join(home, ".tokenops", "recovery")
			}
			path := filepath.Join(dir, id+".out")
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("recover: no stored output for id %q (%w)", id, err)
			}
			command := lookupRecoveryCommand(recoverDir, id)
			// Record the access before printing so the signal is durable.
			_ = appendLearnRecord(recoverDir, fmtlearn.Record{
				Type: fmtlearn.RecordAccess, ID: id, Command: command, TS: time.Now().UTC(),
			})
			_, _ = cmd.OutOrStdout().Write(data)
			return nil
		},
	}
	cmd.Flags().StringVar(&recoverDir, "recover-dir", "", "recovery store dir (defaults to ~/.tokenops/recovery)")
	return cmd
}

// lookupRecoveryCommand finds the command a recovery id belongs to by
// scanning the index; "" when unknown (Analyze still attributes via id).
func lookupRecoveryCommand(recoverDir, id string) string {
	recs, err := readLearnRecords(recoverDir)
	if err != nil {
		return ""
	}
	for _, r := range recs {
		if r.Type == fmtlearn.RecordCompress && r.ID == id {
			return r.Command
		}
	}
	return ""
}

// newFmtLearnCmd mines the learn index and prints the advisory report:
// which commands to write a formatter for next, and which formatters look
// too aggressive (frequently re-accessed).
func newFmtLearnCmd(rf *rootFlags) *cobra.Command {
	var (
		recoverDir string
		jsonOut    bool
		apply      bool
		configPath string
		noJSONL    bool
		jsonlMax   int
	)
	cmd := &cobra.Command{
		Use:   "learn",
		Short: "Mine fmt telemetry for next-formatter priorities and over-compression",
		Long: `learn analyses the recovery index (from wrapped 'tokenops fmt'
runs) AND your Claude Code logs (~/.claude/projects) — so it reflects your
real usage out of the box, with no commands wrapped and no daemon. It
proposes where the formatter catalog should improve.

Without --apply it is advisory: the formatters stay deterministic and the
output is a report. With --apply it writes the SAFE tuning locally to your
config (optimizer.command_fmt.overrides) — loss-level changes only, which
never touch critical-line rules. New-formatter candidates are printed as a
config stub to paste, never auto-written (they need human-authored regexes).

Use --no-jsonl to restrict to the wrapped-run index only.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			recs, err := readLearnRecords(recoverDir)
			if err != nil {
				return err
			}
			// Self-wiring: fold in signal derived from the Claude Code logs
			// so learn works without any wrapped runs. Best-effort + capped.
			if !noJSONL {
				recs = append(recs, jsonlLearnRecords(rf, jsonlMax)...)
			}
			rep := fmtlearn.Analyze(recs, fmtlearn.Thresholds{})
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			renderLearnReport(cmd, rep)
			if apply {
				return applyLearnHints(cmd, rep, configPath)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&recoverDir, "recover-dir", "", "recovery store dir (defaults to ~/.tokenops/recovery)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the report as JSON")
	cmd.Flags().BoolVar(&apply, "apply", false, "write safe loss-level tuning to config (overrides only)")
	cmd.Flags().StringVar(&configPath, "config", "", "config path to write with --apply (defaults to the standard config path)")
	cmd.Flags().BoolVar(&noJSONL, "no-jsonl", false, "do not fold in signal from Claude Code logs (wrapped-run index only)")
	cmd.Flags().IntVar(&jsonlMax, "jsonl-max", 150, "cap Claude Code sessions scanned for learn (newest first); 0 = all")
	return cmd
}

// applyLearnHints writes the safe part of the report — per-command loss-level
// overrides — into the user's config. Level tuning cannot drop a critical
// line (the critical rules are unchanged), so it is safe to auto-apply
// locally. New-formatter candidates are printed as a paste-ready stub.
func applyLearnHints(cmd *cobra.Command, rep fmtlearn.Report, configPath string) error {
	out := cmd.OutOrStdout()
	if configPath == "" {
		p, err := defaultConfigPath()
		if err != nil {
			return err
		}
		configPath = p
	}
	cfg, err := readMutableConfig(configPath)
	if err != nil {
		return fmt.Errorf("learn --apply: read config: %w", err)
	}

	// Current effective policy so we can step levels relative to it.
	policy, _ := buildLossPolicy(cfg.Optimizer.CommandFmt, "")
	if cfg.Optimizer.CommandFmt.Overrides == nil {
		cfg.Optimizer.CommandFmt.Overrides = map[string]string{}
	}

	changed := 0
	for _, h := range rep.LevelHints {
		cur := policy.LevelFor(h.Command)
		next := cur
		switch h.Suggestion {
		case "lower":
			if cur > formatter.LossConservative {
				next = cur - 1
			}
		case "raise":
			if cur < formatter.LossAggressive {
				next = cur + 1
			}
		}
		if next == cur {
			continue
		}
		cfg.Optimizer.CommandFmt.Overrides[h.Command] = next.String()
		fmt.Fprintf(out, "  set command_fmt.overrides[%s] = %s (was %s: %s)\n",
			h.Command, next.String(), cur.String(), h.Suggestion)
		changed++
	}

	if changed == 0 {
		fmt.Fprintln(out, "\nNo safe level changes to apply.")
	} else {
		if err := writeMutableConfig(configPath, cfg); err != nil {
			return fmt.Errorf("learn --apply: write config: %w", err)
		}
		fmt.Fprintf(out, "\nApplied %d level override(s) to %s\n", changed, configPath)
	}

	// Print (never auto-write) a config stub for unknown commands.
	if len(rep.NextFormatters) > 0 {
		fmt.Fprintln(out, "\nCandidate config formatters to write (paste under optimizer.command_fmt.formatters):")
		for _, c := range rep.NextFormatters {
			fmt.Fprintf(out, "  - command: %s\n", c.Command)
			fmt.Fprintf(out, "      critical: [\"(?i)error\", \"(?i)fail\"]   # edit: lines to always keep\n")
			fmt.Fprintf(out, "      drop:\n")
			fmt.Fprintf(out, "        balanced: [\"^DEBUG \", \"^INFO \"]      # edit: noise to drop\n")
		}
	}
	return nil
}

func renderLearnReport(cmd *cobra.Command, rep fmtlearn.Report) {
	out := cmd.OutOrStdout()
	if rep.TotalRuns == 0 {
		fmt.Fprintln(out, "No fmt telemetry yet. Run `tokenops fmt -- <cmd>` (recovery enabled) to gather data.")
		return
	}
	fmt.Fprintf(out, "fmt learning report — %d runs, %d re-accesses, ~%d tokens saved\n\n",
		rep.TotalRuns, rep.TotalAccesses, rep.TokensSaved)

	if len(rep.NextFormatters) > 0 {
		fmt.Fprintln(out, "Next formatters to write (commands falling back to the generic scrub):")
		fmt.Fprintf(out, "  %-14s %6s %8s %10s\n", "COMMAND", "RUNS", "GENERIC%", "RAW BYTES")
		for _, c := range rep.NextFormatters {
			fmt.Fprintf(out, "  %-14s %6d %7.0f%% %10d\n", c.Command, c.Runs, 100*c.GenericRatio, c.RawBytes)
		}
		fmt.Fprintln(out)
	}
	if len(rep.CriticalMisses) > 0 {
		fmt.Fprintln(out, "Possible over-compression (compact output re-fetched often — tighten rules / lower level):")
		fmt.Fprintf(out, "  %-14s %6s %11s\n", "COMMAND", "RUNS", "REACCESS%")
		for _, c := range rep.CriticalMisses {
			fmt.Fprintf(out, "  %-14s %6d %10.0f%%\n", c.Command, c.Runs, 100*c.AccessRate)
		}
		fmt.Fprintln(out)
	}
	if len(rep.LevelHints) > 0 {
		fmt.Fprintln(out, "Loss-level tuning hints:")
		for _, h := range rep.LevelHints {
			fmt.Fprintf(out, "  %-14s %-6s (%s)\n", h.Command, h.Suggestion, h.Rationale)
		}
		fmt.Fprintln(out)
	}
	if len(rep.NextFormatters) == 0 && len(rep.CriticalMisses) == 0 && len(rep.LevelHints) == 0 {
		fmt.Fprintln(out, "No actionable signal yet — catalog coverage looks healthy for the observed traffic.")
	}
}
