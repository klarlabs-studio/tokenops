package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/optimizer"
	"github.com/felixgeelhaar/tokenops/internal/optimizer/contexttrim"
	"github.com/felixgeelhaar/tokenops/internal/optimizer/dedupe"
	"github.com/felixgeelhaar/tokenops/internal/optimizer/promptcompress"
	"github.com/felixgeelhaar/tokenops/internal/optimizer/retrievalprune"
	"github.com/felixgeelhaar/tokenops/internal/replay"
	"github.com/felixgeelhaar/tokenops/internal/spend"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/internal/tokenizer"
	"github.com/felixgeelhaar/tokenops/internal/waste"
	"github.com/felixgeelhaar/tokenops/internal/workflow"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// newReplayCmd builds the `tokenops replay` subcommand.
func newReplayCmd(rf *rootFlags) *cobra.Command {
	var (
		dbPath     string
		workflowID string
		agentID    string
		sinceFlag  string
		untilFlag  string
		limit      int
		jsonOut    bool
	)
	cmd := &cobra.Command{
		Use:   "replay [SESSION_ID]",
		Short: "Replay a session through the optimizer pipeline",
		Long: `Replay loads PromptEvents from the local event store and runs the
optimizer pipeline against them in replay mode. The original requests are
never resent upstream — replay is offline introspection.

Output: per-step token/spend savings, optimization recommendations, waste
findings (when --workflow is set), and a summary footer.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			path, err := resolveStorageReadPath(dbPath, cfg.Storage.Path)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := sqlite.Open(ctx, path, sqlite.Options{})
			if err != nil {
				return fmt.Errorf("open event store: %w", err)
			}
			defer func() { _ = store.Close() }()

			sel := replay.SessionSelector{
				WorkflowID: workflowID,
				AgentID:    agentID,
				Limit:      limit,
			}
			if len(args) == 1 {
				sel.SessionID = args[0]
			}
			if sinceFlag != "" {
				since, err := parseSince(sinceFlag)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				sel.Since = since
			}
			if untilFlag != "" {
				until, err := time.Parse(time.RFC3339, untilFlag)
				if err != nil {
					return fmt.Errorf("--until: %w", err)
				}
				sel.Until = until
			}
			if sel.WorkflowID == "" && sel.SessionID == "" && sel.AgentID == "" {
				return errors.New("provide a SESSION_ID positional or --workflow/--agent flag")
			}

			spendEng := spend.NewEngine(spend.DefaultTable())
			pipeline := buildReplayPipeline(spendEng)
			eng := replay.New(store, pipeline, spendEng)

			res, err := eng.Replay(ctx, sel)
			if err != nil {
				if errors.Is(err, replay.ErrEmptySession) {
					return fmt.Errorf("no prompt events matched the selector")
				}
				return err
			}

			var coachings []*eventschema.CoachingEvent
			if sel.WorkflowID != "" {
				trace, err := workflow.Reconstruct(ctx, store, spendEng, sel.WorkflowID)
				if err == nil && trace != nil {
					coachings = waste.New(waste.Config{}).Detect(trace)
				}
			}

			if jsonOut {
				return writeReplayJSON(cmd.OutOrStdout(), sel, res, coachings)
			}
			return writeReplayText(cmd.OutOrStdout(), sel, res, coachings, spendEng)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to config.storage.path)")
	cmd.Flags().StringVar(&workflowID, "workflow", "", "filter by workflow id")
	cmd.Flags().StringVar(&agentID, "agent", "", "filter by agent id")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "lower bound: RFC3339 timestamp or duration like 24h")
	cmd.Flags().StringVar(&untilFlag, "until", "", "upper bound (RFC3339 timestamp)")
	cmd.Flags().IntVar(&limit, "limit", 1000, "max prompts to replay")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	return cmd
}

// buildReplayPipeline constructs the standard optimizer pipeline used by
// `tokenops replay`. All optimizers run in passive/replay mode (the pipeline
// drives the decision based on req.Mode), so they only emit recommendations.
func buildReplayPipeline(_ *spend.Engine) *optimizer.Pipeline {
	tk := tokenizer.NewRegistry()
	return optimizer.NewPipeline(
		promptcompress.New(promptcompress.Config{}, tk),
		dedupe.New(dedupe.Config{}, tk),
		retrievalprune.New(retrievalprune.Config{}, tk),
		contexttrim.New(contexttrim.Config{}, tk),
	)
}

// resolveStorageReadPath picks the sqlite path for the replay command.
// Order: --db flag, config.storage.path, ~/.tokenops/events.db.
func resolveStorageReadPath(flagPath, configPath string) (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	if configPath != "" {
		return configPath, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".tokenops", "events.db"), nil
}

// parseSince accepts either an RFC3339 timestamp or a Go duration like
// "24h" / "7d". Durations are subtracted from time.Now().
func parseSince(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// time.ParseDuration does not understand "d"; expand manually.
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil && days > 0 {
			return time.Now().Add(-time.Duration(days) * 24 * time.Hour), nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected RFC3339 or duration, got %q: %w", s, err)
	}
	return time.Now().Add(-d), nil
}

// --- text rendering ----------------------------------------------------

func writeReplayText(
	w io.Writer,
	sel replay.SessionSelector,
	res *replay.Result,
	coachings []*eventschema.CoachingEvent,
	spendEng *spend.Engine,
) error {
	fmt.Fprintf(w, "Replay results — %s\n", describeSelector(sel))
	fmt.Fprintf(w, "  prompts replayed:  %d\n", len(res.Steps))
	fmt.Fprintf(w, "  original input:    %d tokens\n", res.OriginalInputTokens)
	fmt.Fprintf(w, "  original output:   %d tokens\n", res.OriginalOutputTokens)
	fmt.Fprintf(w, "  original spend:    %s\n", fmtMoney(res.OriginalCostUSD, spendEng.Currency()))
	fmt.Fprintf(w, "  estimated savings: %d tokens / %s (%.1f%% of spend)\n\n",
		res.EstimatedSavingsTokens,
		fmtMoney(res.EstimatedSavingsUSD, spendEng.Currency()),
		res.SavingsRatio()*100)

	if len(res.Steps) == 0 {
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STEP\tTIME\tMODEL\tIN\tOUT\tCOST\tSAVE TOK\tSAVE $\tOPTIMIZATIONS")
	for i, step := range res.Steps {
		opts := summariseOptimizations(step.OptimizationEvents)
		fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%d\t%s\t%d\t%s\t%s\n",
			i+1,
			step.OriginalEnvelope.Timestamp.Format("15:04:05"),
			truncate(promptModel(step), 18),
			step.OriginalInputTokens,
			step.OriginalOutputTokens,
			fmtMoney(step.OriginalCostUSD, spendEng.Currency()),
			step.EstimatedSavingsTokens,
			fmtMoney(step.EstimatedSavingsUSD, spendEng.Currency()),
			opts,
		)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if len(coachings) > 0 {
		fmt.Fprintf(w, "\nWaste analysis (workflow %s):\n", sel.WorkflowID)
		for _, c := range coachings {
			fmt.Fprintf(w, "  - [%s] %s\n", c.Kind, c.Summary)
			if c.Details != "" {
				fmt.Fprintf(w, "      %s\n", c.Details)
			}
			if c.EstimatedSavingsTokens > 0 {
				fmt.Fprintf(w, "      estimated savings: %d tokens\n", c.EstimatedSavingsTokens)
			}
		}
	}
	return nil
}

// promptModel returns the model the step was billed against, falling back
// to the requested model.
func promptModel(step replay.StepDiff) string {
	if step.OriginalEnvelope == nil {
		return ""
	}
	pe, ok := step.OriginalEnvelope.Payload.(*eventschema.PromptEvent)
	if !ok {
		return ""
	}
	if pe.ResponseModel != "" {
		return pe.ResponseModel
	}
	return pe.RequestModel
}

func summariseOptimizations(events []*eventschema.OptimizationEvent) string {
	if len(events) == 0 {
		return "(no recommendations)"
	}
	parts := make([]string, 0, len(events))
	for _, ev := range events {
		if ev.EstimatedSavingsTokens == 0 && ev.Reason == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s(-%dtok)", ev.Kind, ev.EstimatedSavingsTokens))
	}
	if len(parts) == 0 {
		return "(no recommendations)"
	}
	return strings.Join(parts, ", ")
}

func describeSelector(s replay.SessionSelector) string {
	parts := make([]string, 0, 4)
	if s.SessionID != "" {
		parts = append(parts, "session="+s.SessionID)
	}
	if s.WorkflowID != "" {
		parts = append(parts, "workflow="+s.WorkflowID)
	}
	if s.AgentID != "" {
		parts = append(parts, "agent="+s.AgentID)
	}
	if !s.Since.IsZero() {
		parts = append(parts, "since="+s.Since.Format(time.RFC3339))
	}
	if !s.Until.IsZero() {
		parts = append(parts, "until="+s.Until.Format(time.RFC3339))
	}
	if len(parts) == 0 {
		return "(no filters)"
	}
	return strings.Join(parts, " ")
}

func fmtMoney(v float64, currency string) string {
	if currency == "" {
		currency = "USD"
	}
	return fmt.Sprintf("%.4f %s", v, currency)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

// --- json rendering ----------------------------------------------------

type replayJSON struct {
	Selector  any                          `json:"selector"`
	Result    *replay.Result               `json:"result"`
	Coaching  []*eventschema.CoachingEvent `json:"coaching,omitempty"`
	Generated time.Time                    `json:"generated_at"`
}

func writeReplayJSON(
	w io.Writer,
	sel replay.SessionSelector,
	res *replay.Result,
	coachings []*eventschema.CoachingEvent,
) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(replayJSON{
		Selector:  sel,
		Result:    res,
		Coaching:  coachings,
		Generated: time.Now().UTC(),
	})
}

// Suppress unused-import if context dropped during refactors.
var _ = context.Background
