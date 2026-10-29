package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// runtimeTurns opens the event store the runtime report's usage and cost
// per task come from. A machine with no store gets the report without
// them, which the report says; any other failure is warned about.
func runtimeTurns(cmd *cobra.Command, rf *rootFlags) (commits.TurnReader, func()) {
	none := func() {}
	cfg, err := loadConfig(rf)
	if err != nil {
		warnRead(cmd, []string{"usage per task is left out: " + err.Error()})
		return nil, none
	}
	path, err := resolvePlanDBIn("", cfg.Storage.Path)
	if err != nil {
		return nil, none
	}
	if _, err := os.Stat(path); err != nil {
		return nil, none
	}
	store, err := sqlite.Open(cmd.Context(), path, sqlite.Options{})
	if err != nil {
		warnRead(cmd, []string{"usage per task is left out: open event store: " + err.Error()})
		return nil, none
	}
	eng, err := buildSpendEngine(cfg)
	if err != nil {
		_ = store.Close()
		warnRead(cmd, []string{"usage per task is left out: " + err.Error()})
		return nil, none
	}
	return spending.NewAggregator(store, eng), func() { _ = store.Close() }
}

// writeRuntime renders `story --runtime`: the coverage report, as text or
// as the derived-only JSON export the tokenops_sessions runtime view
// returns.
func writeRuntime(cmd *cobra.Command, rf *rootFlags, w sessions.Window, limit int, idleGap time.Duration, jsonOut bool) error {
	// The flag's 0 is "all"; the capability's is its default.
	if limit == 0 {
		limit = -1
	}
	turns, closeTurns := runtimeTurns(cmd, rf)
	defer closeTurns()
	rt := sessions.ComputeRuntime(cmd.Context(), w, sessions.RuntimeOptions{Limit: limit, IdleGap: idleGap, Turns: turns}, time.Now())
	warnRead(cmd, rt.Warnings)
	out := cmd.OutOrStdout()
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rt)
	}
	writeRuntimeText(out, rt)
	return nil
}

func writeRuntimeText(w io.Writer, rt sessions.Runtime) {
	fmt.Fprintf(w, "Task runtime — %s\n", rt.Window)
	writeCohortLine(w, &rt.Cohort)
	s := rt.Summary
	if s.Tasks == 0 {
		fmt.Fprintln(w, "  no tasks in this window — widen --days, or check --root and the filters")
		return
	}
	fmt.Fprintf(w, "  %d task%s: %d completed, %d last observed, %d open\n",
		s.Tasks, plural(s.Tasks), s.Status.Completed, s.Status.LastObserved, s.Status.Open)
	fmt.Fprintf(w, "  runtime samples: %d of %d (%d open, %d never answered, left out)\n\n",
		s.RuntimeSamples, s.Tasks, s.ExcludedOpen, s.ExcludedNoTurns)

	fmt.Fprintln(w, "BY COHORT  (observed interval: first instruction to last activity)")
	fmt.Fprintf(w, "  %-12s %-24s %-12s %6s %8s %9s %9s\n", "HARNESS", "MODEL", "ROLE", "TASKS", "SAMPLES", "MEDIAN", "P90")
	for _, g := range rt.Groups {
		model := g.Model
		if model == "" {
			model = "(unrecorded)"
		}
		fmt.Fprintf(w, "  %-12s %-24s %-12s %6d %8d %9s %9s\n", g.Harness, model, g.Role,
			g.Tasks, g.RuntimeSamples, secondsOrUnknown(g.Observed.Median), secondsOrUnknown(g.Observed.P90))
	}

	fmt.Fprintln(w, "\nWHERE THE TIME WENT  (median share of the observed interval, over the samples that record it;")
	fmt.Fprintln(w, "  each task's shares add up to its interval, but medians of them need not add up to 100%)")
	for _, c := range s.Categories {
		share := "unknown"
		if c.Share.Median != nil {
			share = fmt.Sprintf("%.0f%%", *c.Share.Median*100)
		}
		fmt.Fprintf(w, "  %-15s %8s   recorded in %d of %d samples\n", c.Category, share, c.Known, c.Known+c.Unknown)
	}
	if s.ToolCriticalPath.N > 0 {
		fmt.Fprintf(w, "\nTOOLS  critical path (median) %s · summed resource time (median) %s\n",
			secondsOrUnknown(s.ToolCriticalPath.Median), secondsOrUnknown(s.ToolResource.Median))
		fmt.Fprintln(w, "  Concurrent calls overlap: the critical path is what the task waited on, the sum what the tools used.")
	}
	writeRuntimeUsage(w, rt)
	if s.HarnessTTFT.N > 0 {
		fmt.Fprintf(w, "\nTIME TO FIRST TOKEN  (as the harness recorded it) median %s over %d samples\n",
			secondsOrUnknown(s.HarnessTTFT.Median), s.HarnessTTFT.N)
	}
	fmt.Fprintf(w, "\nHeuristic: %d of %d samples show no friction signal — a guess from the transcript, not verified success.\n",
		s.HeuristicClean, s.RuntimeSamples)
	fmt.Fprintf(w, "Percentiles are %s. Completed means the harness recorded the turn ending, not that the work was right.\n",
		s.Observed.Method)
	fmt.Fprintln(w, rt.Caution)
	fmt.Fprintln(w, "\nNot measured:")
	for _, u := range rt.Unmeasured {
		fmt.Fprintf(w, "  · %s\n", u)
	}
	fmt.Fprintln(w, "\nPer-task figures with origins and coverage: tokenops story --runtime --json")
}

// writeRuntimeUsage is the cost per task, over the samples with
// recorded usage; nothing when no turns were read.
func writeRuntimeUsage(w io.Writer, rt sessions.Runtime) {
	s := rt.Summary
	if rt.UsageMethod == "" {
		return
	}
	fmt.Fprintf(w, "\nCOST PER TASK  (recorded turns, at list prices; %d of %d samples have recorded usage)\n",
		s.UsageSamples, s.RuntimeSamples)
	if s.UsageSamples == 0 {
		fmt.Fprintln(w, "  no recorded turn fell in these tasks: their sessions are not in the event store")
		return
	}
	fmt.Fprintf(w, "  value median %s · p90 %s   billed median %s · p90 %s   tokens median %s\n",
		usdOrUnknown(s.APIEquivalentUSD.Median), usdOrUnknown(s.APIEquivalentUSD.P90),
		usdOrUnknown(s.BilledUSD.Median), usdOrUnknown(s.BilledUSD.P90), tokensOrUnknown(s.Tokens.Median))
	if s.UsageUnpriced > 0 {
		fmt.Fprintf(w, "  %d task%s include turns with no list price, so their value understates\n", s.UsageUnpriced, plural(s.UsageUnpriced))
	}
	fmt.Fprintf(w, "  Placed by %s.\n", rt.UsageMethod)
}

func usdOrUnknown(v *float64) string {
	if v == nil {
		return "unknown"
	}
	return fmt.Sprintf("$%.2f", *v)
}

func tokensOrUnknown(v *float64) string {
	if v == nil {
		return "unknown"
	}
	return humanTokens(int64(*v))
}

// secondsOrUnknown renders a duration that may not exist.
func secondsOrUnknown(s *float64) string {
	if s == nil {
		return "unknown"
	}
	if *s == 0 {
		return "0s"
	}
	return humanSeconds(*s)
}
