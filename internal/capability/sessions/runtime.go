package sessions

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
	"go.klarlabs.de/tokenops/internal/contexts/governance/taskruntime"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// The task-runtime domain's types, aliased so it stays their single
// definition.
type (
	// RuntimeTask is one task's runtime decomposition.
	RuntimeTask = taskruntime.Task
	// RuntimeGroup summarises a cohort of tasks.
	RuntimeGroup = taskruntime.Group
)

// RuntimeOptions tunes the runtime report.
type RuntimeOptions struct {
	// Limit is how many tasks, newest first; zero means ten, negative all.
	Limit int
	// IdleGap is the pause that starts a new task; zero takes the
	// default, negative disables splitting.
	IdleGap time.Duration
	// Turns reads the recorded turns each task's usage and cost come
	// from; nil leaves every task's usage null.
	Turns commits.TurnReader
}

// Runtime is a coverage report on where tasks' time went: derived figures
// only, with what the transcripts cannot say named as such.
type Runtime struct {
	Window string     `json:"window"`
	Cohort CohortInfo `json:"cohort"`
	// Tasks are newest first. Summary is over exactly these tasks, and
	// Groups split them by harness, model and role.
	Tasks   []RuntimeTask  `json:"tasks"`
	Summary RuntimeGroup   `json:"summary"`
	Groups  []RuntimeGroup `json:"groups"`
	// UsageMethod is how recorded turns are placed on tasks; empty when
	// no turns were read.
	UsageMethod string `json:"usage_method,omitempty"`
	// Unmeasured names what the transcripts cannot say, so a reader does
	// not take its absence for zero.
	Unmeasured []string `json:"unmeasured"`
	// Caution is how not to read the figures.
	Caution  string   `json:"caution"`
	Note     string   `json:"note,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// unmeasured is what no transcript reader here can measure.
var unmeasured = []string{
	"model_request for codex, cursor and opencode: their transcripts record no request boundaries",
	"tool_execution for cursor and opencode: they record tool calls without their results",
	"approval_wait from permission prompts, for every harness: no transcript records a prompt being shown or answered",
	"time to first token, except where the harness records it (codex task_complete); it is never inferred from timestamps",
	"whether a task succeeded: completed means the harness recorded the turn ending, and nothing here verified the work",
	"usage and cost for a task whose session the event store recorded no turns for: its usage is null, not zero",
}

// ComputeRuntime reconstructs the window's tasks and decomposes each into
// the time the transcripts record. It reads no prompt text: the report is
// derived figures only, safe for any surface.
func ComputeRuntime(ctx context.Context, w Window, opts RuntimeOptions, now time.Time) Runtime {
	limit := opts.Limit
	if limit == 0 {
		limit = 10
	}
	records, window, info, readErr := w.read(now, false)
	gap := opts.IdleGap
	tasks := story.Group(agentdx.Units(records), story.Options{IdleGap: gap})
	analysed := taskruntime.Analyse(tasks, taskruntime.Options{IdleGap: gap, Now: now})
	usageMethod, usageErr := attachUsage(ctx, opts.Turns, tasks, analysed, now)
	// Newest first, then the limit: status needed every task of the
	// session to tell its last one apart.
	for i, j := 0, len(analysed)-1; i < j; i, j = i+1, j-1 {
		analysed[i], analysed[j] = analysed[j], analysed[i]
	}
	if limit > 0 && len(analysed) > limit {
		analysed = analysed[:limit]
	}
	out := Runtime{
		Window: window, Cohort: info, Tasks: analysed,
		Summary: taskruntime.Summarise(analysed), Groups: taskruntime.GroupBy(analysed),
		UsageMethod: usageMethod, Unmeasured: unmeasured,
		Caution: "Intervals are descriptive, not model runtime. Never add task durations up as time spent " +
			"waiting: concurrent tasks overlap, and idle and unattributed time is nobody's work.",
		Warnings: readWarnings(readErr, true),
	}
	if usageErr != nil {
		out.Warnings = append(out.Warnings, "usage and cost per task could not be read from the event store: "+usageErr.Error())
	}
	if len(analysed) == 0 {
		out.Note = "no tasks in this window — widen with days or all, or check the transcript root and filters"
	}
	return out
}

// attachUsage reads the recorded turns over the tasks' span and places
// them on the tasks. It returns the method used, empty when no turns were
// read.
func attachUsage(ctx context.Context, r commits.TurnReader, tasks []story.Task, analysed []RuntimeTask, now time.Time) (string, error) {
	if r == nil || len(tasks) == 0 {
		return "", nil
	}
	since := tasks[0].Start
	for _, t := range tasks {
		if t.Start.Before(since) {
			since = t.Start
		}
	}
	recorded, err := r.SessionTurns(ctx, analytics.Filter{Since: since, Until: now})
	if err != nil {
		return "", err
	}
	turns := make([]taskruntime.Turn, 0, len(recorded))
	for _, t := range recorded {
		turns = append(turns, taskruntime.Turn{
			SessionID: t.SessionID, At: t.At,
			InputTokens: t.InputTokens, OutputTokens: t.OutputTokens,
			CacheReadTokens: t.CacheReadTokens, CacheWriteTokens: t.CacheWriteTokens,
			BilledUSD: t.BilledUSD(), APIEquivalentUSD: t.APIEquivalentUSD, Priced: t.Priced,
		})
	}
	taskruntime.AttachUsage(tasks, analysed, turns)
	return taskruntime.UsageMethod, nil
}
