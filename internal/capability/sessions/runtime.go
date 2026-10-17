package sessions

import (
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
	"go.klarlabs.de/tokenops/internal/contexts/governance/taskruntime"
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
}

// ComputeRuntime reconstructs the window's tasks and decomposes each into
// the time the transcripts record. It reads no prompt text: the report is
// derived figures only, safe for any surface.
func ComputeRuntime(w Window, opts RuntimeOptions, now time.Time) Runtime {
	limit := opts.Limit
	if limit == 0 {
		limit = 10
	}
	records, window, info, readErr := w.read(now, false)
	gap := opts.IdleGap
	tasks := story.Group(agentdx.Units(records), story.Options{IdleGap: gap})
	analysed := taskruntime.Analyse(tasks, taskruntime.Options{IdleGap: gap, Now: now})
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
		Unmeasured: unmeasured,
		Caution: "Intervals are descriptive, not model runtime. Never add task durations up as time spent " +
			"waiting: concurrent tasks overlap, and idle and unattributed time is nobody's work.",
		Warnings: readWarnings(readErr, true),
	}
	if len(analysed) == 0 {
		out.Note = "no tasks in this window — widen with days or all, or check the transcript root and filters"
	}
	return out
}
