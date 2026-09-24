package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/reconstruct"
	"go.klarlabs.de/tokenops/internal/capability/verify"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// newVerifyCmd compares outcomes and resource use across real executions.
// It uses linked randomized assignments only when the selected trial has
// complete execution pairs, measured tokens, and explicit outcomes;
// otherwise the result remains explicitly observational.
func newVerifyCmd() *cobra.Command {
	var (
		days         int
		jsonOut      bool
		dbPath       string
		idleGap      string
		showEach     bool
		experimentID string
	)
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Compare resource use and outcomes across optimized attempts",
		Long: `verify attributes recorded events to reconstructed attempts. By default,
it compares attempts an optimization touched against those it did not; this is
observational and does not establish causality. When execution-linked randomized
assignments are available, it can instead compare a complete experiment's paired
arms. Use --experiment-id when more than one trial is present. A randomized
comparison requires complete pairs, measured tokens, and explicit outcomes;
otherwise verify falls back to the observational view and explains why.

Explicit human and verifier outcomes linked to an execution are included in
each cohort's assessed success rate. A quality drop can flag harm even when
the token count fell; missing outcomes remain unknown.

Randomized results are evidence about the selected experiment, not a guarantee
for other work.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report, err := runVerify(cmd, verifyOptions{
				days: days, dbPath: dbPath, idleGap: idleGap, showEach: showEach, experimentID: experimentID,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			writeVerifyText(cmd.OutOrStdout(), report)
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 30, "window in days; 0 reads everything")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to ~/.tokenops/events.db)")
	cmd.Flags().StringVar(&idleGap, "idle-gap", "", "pause that starts a new attempt (default 10m)")
	cmd.Flags().BoolVar(&showEach, "each", false, "list every attempt, not only the comparison")
	cmd.Flags().StringVar(&experimentID, "experiment-id", "", "compare one execution-linked randomized experiment; required when multiple trials are in the window")
	return cmd
}

// writeVerifyText renders the report for a person.
func writeVerifyText(out io.Writer, r verify.Report) {
	if r.RandomizedExperimentID != "" {
		fmt.Fprintf(out, "randomized experiment %s · %d complete pair(s)\n", r.RandomizedExperimentID, r.RandomizedPairs)
	}
	fmt.Fprintf(out, "attempts compared: %d baseline, %d intervention\n\n",
		r.BaselineCount, r.InterventionCount)

	if observed, ok := r.Verdict.Observed.Amount(); ok {
		fmt.Fprintf(out, "  measured difference: %.0f tokens per attempt\n", observed)
	} else {
		fmt.Fprintln(out, "  measured difference: not established")
	}
	baseRate, baseKnown := r.BaselineOutcomes.SuccessRate.Amount()
	interventionRate, interventionKnown := r.InterventionOutcomes.SuccessRate.Amount()
	if baseKnown && interventionKnown {
		fmt.Fprintf(out, "  assessed success: %.0f%% baseline → %.0f%% intervention\n", baseRate, interventionRate)
	} else {
		fmt.Fprintln(out, "  assessed success: not established (record outcomes for executions)")
	}
	baseLatency, baseLatencyKnown := r.BaselineLatency.Amount()
	interventionLatency, interventionLatencyKnown := r.InterventionLatency.Amount()
	if baseLatencyKnown && interventionLatencyKnown {
		fmt.Fprintf(out, "  mean request latency: %.0fms baseline → %.0fms intervention\n", baseLatency, interventionLatency)
	} else {
		fmt.Fprintln(out, "  mean request latency: not established")
	}
	baseCost, baseCostKnown := r.BaselineMeteredCostUSD.Amount()
	interventionCost, interventionCostKnown := r.InterventionMeteredCostUSD.Amount()
	if baseCostKnown && interventionCostKnown {
		fmt.Fprintf(out, "  mean metered cost: $%.6f baseline → $%.6f intervention\n", baseCost, interventionCost)
	} else {
		fmt.Fprintln(out, "  mean metered cost: not established (requires event-time pricing)")
	}
	providers := make(map[string]struct{}, len(r.BaselinePlanQuota)+len(r.InterventionPlanQuota))
	for provider := range r.BaselinePlanQuota {
		providers[provider] = struct{}{}
	}
	for provider := range r.InterventionPlanQuota {
		providers[provider] = struct{}{}
	}
	keys := make([]string, 0, len(providers))
	for provider := range providers {
		keys = append(keys, provider)
	}
	sort.Strings(keys)
	for _, provider := range keys {
		base, baseOK := r.BaselinePlanQuota[provider]
		with, withOK := r.InterventionPlanQuota[provider]
		baseValue, withValue := "not observed", "not observed"
		if baseOK {
			baseValue = fmt.Sprintf("%.0f", base.AmountOr(0))
		}
		if withOK {
			withValue = fmt.Sprintf("%.0f", with.AmountOr(0))
		}
		fmt.Fprintf(out, "  %s plan quota tokens: %s baseline → %s intervention\n", provider, baseValue, withValue)
	}

	// The wording comes from the capability so this and the MCP tool
	// cannot describe the same verdict differently.
	fmt.Fprintf(out, "  reading: %s\n", r.Reading())

	if r.Verdict.Caveat != "" {
		fmt.Fprintf(out, "  why: %s\n", r.Verdict.Caveat)
	}

	if r.Observational() {
		if r.RandomizedFallbackReason != "" {
			fmt.Fprintf(out, "  randomized comparison unavailable: %s\n", r.RandomizedFallbackReason)
		}
		fmt.Fprintln(out, "\nThis is an observational comparison. The cohorts were not")
		fmt.Fprintln(out, "assigned, so the measured difference is not causal evidence.")
	} else {
		fmt.Fprintln(out, "\nCohorts were assigned by the selected randomized experiment.")
		fmt.Fprintln(out, "This is evidence about that experiment, not a guarantee for other work.")
	}
}

// verifyOptions are the switches runVerify was given.
type verifyOptions struct {
	days         int
	dbPath       string
	idleGap      string
	showEach     bool
	experimentID string
}

// runVerify reconstructs the attempts, reads the events, and joins them.
//
// Both halves come from the capability layer: the reconstruction so the
// MCP surface gets the same attempts, and the comparison so neither
// surface can invent its own idea of what a cohort is.
func runVerify(cmd *cobra.Command, opt verifyOptions) (verify.Report, error) {
	gap, err := parseIdleGap(opt.idleGap)
	if err != nil {
		return verify.Report{}, err
	}

	extract := agentdx.ExtractOptions{WithPromptText: true}
	if opt.days > 0 {
		extract.Since = time.Now().AddDate(0, 0, -opt.days)
	}
	records, err := agentdx.ExtractAll(extract)
	if err != nil {
		// A reader that broke is reported; whatever the others yielded is
		// still worth comparing.
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
	}

	reconstructed := reconstruct.FromUnits(agentdx.Units(records), reconstruct.Options{IdleGap: gap})

	events, err := readVerifyEvents(cmd, opt)
	if err != nil {
		return verify.Report{}, err
	}

	report := verify.CompareReconstructedExperiment(reconstructed, events, opt.experimentID)
	if !opt.showEach {
		// The per-attempt detail is the evidence, not the answer. It is
		// long, and an operator asking "did this help" is not asking for
		// a table of every attempt.
		report.Attributed = nil
	}
	return report, nil
}

// readVerifyEvents loads the events the comparison rests on.
func readVerifyEvents(cmd *cobra.Command, opt verifyOptions) ([]*eventschema.Envelope, error) {
	path, err := resolvePlanDB(opt.dbPath)
	if err != nil {
		return nil, err
	}
	// A fresh install has no store, and verify is a plausible first
	// command to try. The library's own error is "sqlite: ping: unable to
	// open database file (14)", which names neither what is missing nor
	// what to do about it.
	if _, statErr := os.Stat(path); statErr != nil {
		return nil, fmt.Errorf(
			"no event store at %s — verify compares recorded work, so there has to be some. "+
				"Run `tokenops init`, enable a source with `tokenops vendor-usage enable "+
				"claude-code-jsonl`, and let the daemon ingest for a while", path)
	}

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}
	defer func() { _ = store.Close() }()

	filter := sqlite.Filter{Limit: 200_000}
	if opt.days > 0 {
		filter.Since = time.Now().AddDate(0, 0, -opt.days)
	}
	return store.Query(ctx, filter)
}

// parseIdleGap reads the flag, defaulting to story's own gap.
func parseIdleGap(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("--idle-gap: %w", err)
	}
	return d, nil
}
