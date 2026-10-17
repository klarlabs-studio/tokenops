package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
)

// writeRuntime renders `story --runtime`: the coverage report, as text or
// as the derived-only JSON export the tokenops_sessions runtime view
// returns.
func writeRuntime(cmd *cobra.Command, w sessions.Window, limit int, idleGap time.Duration, jsonOut bool) error {
	// The flag's 0 is "all"; the capability's is its default.
	if limit == 0 {
		limit = -1
	}
	rt := sessions.ComputeRuntime(w, sessions.RuntimeOptions{Limit: limit, IdleGap: idleGap}, time.Now())
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
