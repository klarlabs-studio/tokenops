package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/reconstruct"
	"go.klarlabs.de/tokenops/internal/capability/verify"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// newVerifyCmd reports whether the optimizations that ran actually
// helped — and, for now, why that question cannot yet be answered.
//
// The honest answer is the feature. TokenOps has always been able to
// report tokens removed; what it could never do was say whether removing
// them made anything better, because nothing joined consumption to an
// attempt at a goal and nothing compared one attempt against another.
//
// That join now exists. What does not yet exist is an assignment: the
// only cohort signal available is whether an optimization happened to
// fire, which is observational, so the difference this prints is real
// and its cause is not established. Printing the number without that
// sentence would be the claim ADR 0004 exists to refuse, with extra
// steps.
func newVerifyCmd() *cobra.Command {
	var (
		days     int
		jsonOut  bool
		dbPath   string
		idleGap  string
		showEach bool
	)
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Compare resource use and outcomes across optimized attempts",
		Long: `verify attributes recorded events to the attempts they were part of, then
compares the attempts an optimization touched against the ones it did not.

It does not tell you an optimization worked. The two groups are split by
whether an optimization happened to fire, which means they differ in ways
that have nothing to do with it — compression applies to large outputs,
routing applies to turns a classifier thought were mechanical. A
difference between such groups is real; attributing it to the
intervention is not warranted.

Explicit human and verifier outcomes linked to an execution are included in
each cohort's assessed success rate. A quality drop can flag harm even when
the token count fell; missing outcomes remain unknown.

What it is for: the difference is the reason to run a real experiment,
and a collapsed success rate is worth acting on whether or not causation
is established.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report, err := runVerify(cmd, verifyOptions{
				days: days, dbPath: dbPath, idleGap: idleGap, showEach: showEach,
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
	return cmd
}

// writeVerifyText renders the report for a person.
func writeVerifyText(out io.Writer, r verify.Report) {
	fmt.Fprintf(out, "attempts compared: %d without an optimization, %d with\n\n",
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

	// The wording comes from the capability so this and the MCP tool
	// cannot describe the same verdict differently.
	fmt.Fprintf(out, "  reading: %s\n", r.Reading())

	if r.Verdict.Caveat != "" {
		fmt.Fprintf(out, "  why: %s\n", r.Verdict.Caveat)
	}

	fmt.Fprintln(out, "\nThis is a comparison, not an experiment. To establish that an")
	fmt.Fprintln(out, "optimization helped, TokenOps would have to decide which attempts")
	fmt.Fprintln(out, "receive it rather than observe which ones happened to.")
}

// verifyOptions are the switches runVerify was given.
type verifyOptions struct {
	days     int
	dbPath   string
	idleGap  string
	showEach bool
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

	report := verify.CompareReconstructed(reconstructed, events)
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
