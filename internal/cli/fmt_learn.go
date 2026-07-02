package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/fmtlearn"
)

// recoveryIndexPath returns the learn-index file inside the recovery dir.
// The index is an append-only JSONL of compress + access records that the
// offline learning loop (`tokenops fmt learn`) mines.
func recoveryIndexPath(recoverDir string) (string, error) {
	if recoverDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		recoverDir = filepath.Join(home, ".tokenops", "recovery")
	}
	return filepath.Join(recoverDir, "index.jsonl"), nil
}

// appendLearnRecord appends one record to the learn index. Best-effort: the
// caller ignores errors so telemetry never breaks a wrapped command.
func appendLearnRecord(recoverDir string, rec fmtlearn.Record) error {
	path, err := recoveryIndexPath(recoverDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	return enc.Encode(rec)
}

// readLearnRecords reads every record from the learn index. A missing index
// is not an error — it yields an empty slice.
func readLearnRecords(recoverDir string) ([]fmtlearn.Record, error) {
	path, err := recoveryIndexPath(recoverDir)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var recs []fmtlearn.Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r fmtlearn.Record
		if err := json.Unmarshal(line, &r); err != nil {
			continue // skip malformed rows rather than abort
		}
		recs = append(recs, r)
	}
	return recs, sc.Err()
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
func newFmtLearnCmd() *cobra.Command {
	var (
		recoverDir string
		jsonOut    bool
	)
	cmd := &cobra.Command{
		Use:   "learn",
		Short: "Mine fmt telemetry for next-formatter priorities and over-compression",
		Long: `learn analyses the append-only recovery index (compression +
re-access records) and proposes where the formatter catalog should improve.
It never changes runtime behaviour — the formatters stay deterministic; the
output is advisory, to be turned into corpus-gated code changes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			recs, err := readLearnRecords(recoverDir)
			if err != nil {
				return err
			}
			rep := fmtlearn.Analyze(recs, fmtlearn.Thresholds{})
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			renderLearnReport(cmd, rep)
			return nil
		},
	}
	cmd.Flags().StringVar(&recoverDir, "recover-dir", "", "recovery store dir (defaults to ~/.tokenops/recovery)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the report as JSON")
	return cmd
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
