package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/fmtinsight"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// newFmtExperimentCmd shows or sets the fmt outcome experiment: a share
// of sessions, drawn at random by session id, get their command output
// raw, and the arms are compared on how the work went.
func newFmtExperimentCmd(rf *rootFlags) *cobra.Command {
	var (
		days       int
		jsonOut    bool
		recoverDir string
		dbPath     string
	)
	cmd := &cobra.Command{
		Use:   "experiment [off|on|<percent>]",
		Short: "Show or set the experiment that judges fmt on outcomes, not bytes saved",
		Long: `experiment runs some sessions on raw command output at random and
compares how the work went in each arm: first-try and reworked instructions,
repeated tool calls, tokens per instruction, and outcomes recorded with
'tokenops outcome' under the session id. Bytes saved say what fmt removed,
not whether the agent still found what it needed.

'on' sends half the sessions raw; a percent from 0 to 50 sets the share;
'off' (the default) stops it. Each session's arm is drawn from its id, read
from TOKENOPS_SESSION_ID or Claude Code's CLAUDE_CODE_SESSION_ID; a command
run without one is compressed as usual and left out. In the raw arm fmt
forwards stdout unchanged, prints no stats line, and records what it would
have saved.

With no argument it shows the setting and the comparison. Each arm needs 4
sessions and 20 instructions before anything is compared; each difference
comes with a rough 95% interval over sessions, and one that excludes zero
is a lead to check, not a finding.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				n, err := fmtinsight.ParseExperiment(args[0])
				if err != nil {
					return err
				}
				if err := setFmtExperiment(rf, n); err != nil {
					return err
				}
			}
			percent := commandFmtConfig(rf).Experiment
			since := time.Time{}
			if days > 0 {
				since = time.Now().Add(-time.Duration(days) * 24 * time.Hour)
			}
			rep, err := fmtinsight.Experiment(percent, fmtinsight.ExperimentOptions{
				RecoverDir: recoverDir, Since: since, Outcomes: fmtExperimentOutcomes(cmd.Context(), dbPath, since),
			})
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			renderFmtExperiment(cmd.OutOrStdout(), rep)
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", fmtinsight.DefaultExperimentDays, "window in days; 0 reads everything")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the report as JSON")
	cmd.Flags().StringVar(&recoverDir, "recover-dir", "", "recovery store dir (defaults to ~/.tokenops/recovery)")
	cmd.Flags().StringVar(&dbPath, "db", "", "events.db the recorded outcomes are read from (defaults to the configured store)")
	return cmd
}

// setFmtExperiment writes the experiment share to the config file.
func setFmtExperiment(rf *rootFlags, percent int) error {
	path, err := resolveMutableConfigPath(rf.configPath)
	if err != nil {
		return err
	}
	cfg, err := readMutableConfig(path)
	if err != nil {
		return err
	}
	cfg.Optimizer.CommandFmt.Experiment = percent
	return writeMutableConfig(path, cfg)
}

// fmtExperimentOutcomes reads the recorded outcomes. The store is
// optional: without one the comparison stands on the transcripts alone.
func fmtExperimentOutcomes(ctx context.Context, override string, since time.Time) map[string]string {
	path, err := resolvePlanDB(override)
	if err != nil {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		return nil
	}
	defer func() { _ = store.Close() }()
	events, err := store.ReadEvents(ctx, eventschema.EventTypeOutcome, since)
	if err != nil {
		return nil
	}
	return fmtinsight.ExperimentOutcomes(events)
}

// renderFmtExperiment prints the setting and the comparison.
func renderFmtExperiment(out io.Writer, r fmtinsight.ExperimentReport) {
	if r.RawPercent > 0 {
		fmt.Fprintf(out, "fmt experiment: on, %d%% of sessions get raw command output (drawn by session id)\n", r.RawPercent)
	} else {
		fmt.Fprintln(out, "fmt experiment: off (`tokenops fmt experiment on` starts it)")
	}
	for _, line := range fmtinsight.ExperimentLines(r) {
		fmt.Fprintln(out, line)
	}
}
