package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/capability/workquote"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

func newTaskQuoteCmd(rf *rootFlags) *cobra.Command {
	var (
		model, effort, client, provider, dbPath string
		jsonOut                                 bool
	)
	cmd := &cobra.Command{
		Use:   "quote <instruction>",
		Short: "Price a task before it starts, from your own similar work",
		Long: `quote prices a task before it starts: what instructions like it cost
you at list prices over the last 30 days (the median and the 90th
percentile), the tokens they processed, the share of each current plan
window that would take, and any boundary a p90 task would meet.

It is an estimate drawn from your own history on the same model (and
effort and kind of work where known), never a promise. With fewer than 20
such instructions it gives no number. The instruction is classified here
for its kind of work and never stored.

  tokenops task quote "fix the flaky retry test" --model claude-opus-4-5

Agents get the same quote from tokenops_prepare_work, which records it so
tokenops_review_work can check it against what the task took; the
calibration line says how often quoted tasks stayed within their p90.`,
		Example: `  tokenops task quote "fix the flaky retry test" --model claude-opus-4-5
  tokenops task quote "design the cache layer" --model claude-opus-4-5 --effort high --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(model) == "" {
				return errors.New("--model is required: a quote is drawn from instructions on the same model")
			}
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			path, err := resolveStorageReadPath(dbPath, cfg.Storage.Path)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openStoreForRead(ctx, path)
			if err != nil {
				return fmt.Errorf("open event store: %w", err)
			}
			defer func() { _ = store.Close() }()
			now := time.Now().UTC()
			basis, err := quoteBasis(ctx, cfg, store, now)
			if err != nil {
				return err
			}
			deps := workquote.Deps{Basis: basis, Rules: bcap.AllRules(cfg), Reports: quoteReports(ctx, cfg, store, now)}
			res, err := workquote.Prepare(ctx, deps, workquote.Request{
				Instruction: args[0], Model: model, Effort: effort, Client: client, Provider: provider,
			}, now)
			if err != nil {
				return err
			}
			if res.Calibration, err = workquote.Calibration(ctx, store, now); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
			}
			writeQuote(cmd.OutOrStdout(), res)
			return nil
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "the model the task will run on (required)")
	cmd.Flags().StringVar(&effort, "effort", "", "the reasoning effort it will run at, when known")
	cmd.Flags().StringVar(&client, "client", "", "only draw on one client's instructions (claude-code, codex, opencode)")
	cmd.Flags().StringVar(&provider, "provider", "", "the plan whose windows to set the quote against (default: the provider most of the history went to)")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to config.storage.path)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

// quoteBasis is the basis the daemon keeps, or, when it has kept none
// yet, one computed here and not stored.
func quoteBasis(ctx context.Context, cfg config.Config, store *sqlite.Store, now time.Time) (*workquote.Basis, error) {
	if b, err := workquote.Read(workquote.DefaultDir()); err == nil && b != nil && b.Version == workquote.Version {
		return b, nil
	}
	eng, err := buildSpendEngine(cfg)
	if err != nil {
		return nil, err
	}
	b, err := workquote.Compute(ctx, workquote.InputsFor(spending.NewAggregator(store, eng), store), now)
	if err != nil {
		return nil, fmt.Errorf("compute the quote's history: %w", err)
	}
	return &b, nil
}

// quoteReports are the plans' current windows: the daemon's when it
// answers, else computed here.
func quoteReports(ctx context.Context, cfg config.Config, store *sqlite.Store, now time.Time) []plans.HeadroomReport {
	var fromDaemon headroom.HeadroomPayload
	if askDaemon(ctx, cfg, "/api/plans/headroom", 5*time.Second, &fromDaemon) {
		return fromDaemon.Reports
	}
	res, err := headroom.Compute(ctx, localHeadroomDeps(&cfg, store), now)
	if err != nil {
		return nil
	}
	return res.Reports
}

func writeQuote(w io.Writer, r *workquote.Result) {
	fmt.Fprintf(w, "Quote (an estimate): %s\n", r.Summary)
	if r.Quoted() {
		fmt.Fprintf(w, "  cost at list prices   $%.2f typical · $%.2f p90\n", r.P50USD, r.P90USD)
		fmt.Fprintf(w, "  tokens processed      %d typical · %d p90\n", r.P50Tokens, r.P90Tokens)
	}
	for _, s := range r.Windows {
		name := s.Window
		if s.Provider != "" {
			name = s.Provider + " " + s.Window
		}
		fmt.Fprintf(w, "  %-21s %s\n", name, s.Note)
	}
	for _, t := range r.Boundaries {
		stage := "enforced"
		if !t.Enforced {
			stage = "shadow"
		}
		when := "would meet"
		if t.Already {
			when = "already meets"
		}
		fmt.Fprintf(w, "  boundary %s (%s, %s): %s %s\n", t.Rule, t.Require, stage, when, t.Crossed)
	}
	if r.Approval != "" {
		fmt.Fprintf(w, "\n%s\n", r.Approval)
	}
	fmt.Fprintf(w, "\nCalibration: cost — %s; window share — %s.\n", r.Calibration.Cost.Note, r.Calibration.Share.Note)
	if !r.BasisComputedAt.IsZero() {
		fmt.Fprintf(w, "History as of %s.\n", r.BasisComputedAt.Local().Format("2 Jan 15:04"))
	}
}
