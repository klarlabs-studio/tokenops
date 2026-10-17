package taskruntime

import (
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
)

// All fixtures are fictional: invented sessions at invented instants.
var t0 = time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

// rec builds one record of a fictional session.
type rec = agentdx.Record

func prompt(s string, sec int, h agentdx.Source) rec {
	return rec{At: at(sec), SessionID: s, Kind: agentdx.KindPrompt, Harness: h}
}

func turn(s string, sec int, req string) rec {
	return rec{At: at(sec), SessionID: s, Kind: agentdx.KindAssistantTurn, RequestID: req, Model: "model-a"}
}

func call(s string, sec int, id, name string) rec {
	return rec{At: at(sec), SessionID: s, Kind: agentdx.KindToolUse, CallID: id, ToolName: name}
}

func result(s string, sec int, id string) rec {
	return rec{At: at(sec), SessionID: s, Kind: agentdx.KindToolResult, CallID: id}
}

func turnEnd(s string, sec int) rec {
	return rec{At: at(sec), SessionID: s, Kind: agentdx.KindTurnEnd, DurationMs: 1000}
}

func analyse(t *testing.T, recs []rec, opts Options) []Task {
	t.Helper()
	return Analyse(story.Group(agentdx.Units(recs), story.Options{IdleGap: opts.IdleGap}), opts)
}

func category(t *testing.T, task Task, c Category) CategoryTime {
	t.Helper()
	for _, ct := range task.Categories {
		if ct.Category == c {
			return ct
		}
	}
	t.Fatalf("no %s in %+v", c, task.Categories)
	return CategoryTime{}
}

func secs(t *testing.T, ct CategoryTime) float64 {
	t.Helper()
	if ct.Seconds == nil {
		t.Fatalf("%s is unknown, want a figure", ct.Category)
	}
	return *ct.Seconds
}

// The known categories always add up to the observed interval: every
// instant is assigned once, so overlap cannot be counted twice.
func assertAddsUp(t *testing.T, task Task) {
	t.Helper()
	var sum float64
	for _, ct := range task.Categories {
		if ct.Seconds != nil {
			sum += *ct.Seconds
		}
	}
	if task.ObservedSeconds == nil || math.Abs(sum-*task.ObservedSeconds) > 1e-9 {
		t.Errorf("categories sum to %v, observed %v", sum, task.ObservedSeconds)
	}
}

// Two tools running at once: the task waited for the longer stretch they
// covered together, while the tools used the sum of their spans.
func TestOverlappingToolsReportCriticalPathAndResourceTimeApart(t *testing.T) {
	cc := agentdx.SourceClaudeCode
	tasks := analyse(t, []rec{
		prompt("s", 0, cc),
		turn("s", 10, "r1"), call("s", 10, "a", "Bash"),
		turn("s", 12, "r1"), call("s", 12, "b", "Bash"),
		result("s", 40, "a"), result("s", 52, "b"),
		turn("s", 62, "r2"), turnEnd("s", 63),
	}, Options{})
	task := tasks[0]
	if task.Tools.CriticalPathSeconds == nil || *task.Tools.CriticalPathSeconds != 42 {
		t.Errorf("critical path = %v, want 42 (10s..52s)", task.Tools.CriticalPathSeconds)
	}
	if task.Tools.ResourceSeconds == nil || *task.Tools.ResourceSeconds != 70 {
		t.Errorf("resource = %v, want 70 (30 + 40)", task.Tools.ResourceSeconds)
	}
	if task.Tools.MaxConcurrent != 2 {
		t.Errorf("max concurrent = %d, want 2", task.Tools.MaxConcurrent)
	}
	// Tool time wins the 2s where r1 was still writing while a ran.
	if got := secs(t, category(t, task, CategoryToolExecution)); got != 42 {
		t.Errorf("tool_execution = %v, want 42", got)
	}
	if got := secs(t, category(t, task, CategoryModelRequest)); got != 20 {
		t.Errorf("model_request = %v, want 20 (0..10, 52..62)", got)
	}
	if task.Status != StatusCompleted {
		t.Errorf("status = %s, want completed", task.Status)
	}
	assertAddsUp(t, task)
}

// A pause between instructions is the operator not having typed yet, and
// a machine asleep in the middle of a request is nobody's work: neither
// is the model's.
func TestLongGapsAreNotModelTime(t *testing.T) {
	cc := agentdx.SourceClaudeCode
	tasks := analyse(t, []rec{
		prompt("s", 0, cc), turn("s", 30, "r1"),
		// Five minutes later, within the idle gap: the same task.
		prompt("s", 330, cc), turn("s", 340, "r2"), call("s", 340, "a", "Read"),
		result("s", 345, "a"),
		// The laptop slept for an hour and a half before the reply.
		turn("s", 345+5400, "r3"),
	}, Options{})
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	task := tasks[0]
	if got := secs(t, category(t, task, CategoryIdle)); got != 300 {
		t.Errorf("idle = %v, want 300", got)
	}
	model := category(t, task, CategoryModelRequest)
	if got := secs(t, model); got != 40 {
		t.Errorf("model_request = %v, want 40: the 90-minute span must not count", got)
	}
	if model.Complete {
		t.Errorf("model_request complete despite a dropped span")
	}
	if got := secs(t, category(t, task, CategoryUnattributed)); got != 5400 {
		t.Errorf("unattributed = %v, want 5400", got)
	}
	assertAddsUp(t, task)

	// Past the idle gap the pause starts a new task instead.
	split := analyse(t, []rec{
		prompt("s", 0, cc), turn("s", 30, "r1"),
		prompt("s", 3600, cc), turn("s", 3630, "r2"),
	}, Options{})
	if len(split) != 2 || split[1].Boundary != string(story.BoundaryIdle) {
		t.Fatalf("tasks = %+v, want two split by the idle gap", split)
	}
}

// A question to the operator is approval waiting, measured; it is a lower
// bound, because permission prompts are not recorded at all.
func TestApprovalWaitIsALowerBound(t *testing.T) {
	cc := agentdx.SourceClaudeCode
	task := analyse(t, []rec{
		prompt("s", 0, cc), turn("s", 5, "r1"), call("s", 5, "q", "AskUserQuestion"),
		result("s", 245, "q"), turn("s", 250, "r2"),
	}, Options{})[0]
	wait := category(t, task, CategoryApprovalWait)
	if got := secs(t, wait); got != 240 {
		t.Errorf("approval_wait = %v, want 240", got)
	}
	if wait.Complete || wait.Caveat == "" {
		t.Errorf("approval_wait claims to be complete: %+v", wait)
	}
	if got := secs(t, category(t, task, CategoryToolExecution)); got != 0 {
		t.Errorf("tool_execution = %v, want 0: the question is a wait, not a tool", got)
	}
	assertAddsUp(t, task)
}

// A harness that records no request boundaries has no model figure. It
// is unknown, not zero, and the time sits visibly in unattributed.
func TestAbsentModelSpansAreUnknownNotZero(t *testing.T) {
	cx := agentdx.SourceCodex
	task := analyse(t, []rec{
		prompt("s", 0, cx), call("s", 5, "c1", "shell"), result("s", 15, "c1"),
		{At: at(20), SessionID: "s", Kind: agentdx.KindAssistantTurn, Model: "model-b"},
		{At: at(20), SessionID: "s", Kind: agentdx.KindTurnEnd, DurationMs: 20000, TTFTMs: 900, TTFTRecorded: true},
	}, Options{})[0]
	model := category(t, task, CategoryModelRequest)
	if model.Seconds != nil || model.Share != nil || model.Quality != measurement.QualityUnknown {
		t.Errorf("model_request = %+v, want unknown", model)
	}
	approval := category(t, task, CategoryApprovalWait)
	if approval.Seconds != nil {
		t.Errorf("approval_wait = %v from a rollout that records no approvals", *approval.Seconds)
	}
	un := category(t, task, CategoryUnattributed)
	if got := secs(t, un); got != 10 || un.Caveat == "" {
		t.Errorf("unattributed = %v (%q), want 10 with a caveat naming the unknowns", got, un.Caveat)
	}
	if task.HarnessTTFTSeconds == nil || *task.HarnessTTFTSeconds != 0.9 {
		t.Errorf("harness TTFT = %v, want the recorded 0.9s", task.HarnessTTFTSeconds)
	}
	assertAddsUp(t, task)
}

// An instruction nobody answered is no runtime sample, and a task still
// in flight is right-censored: neither enters a duration percentile, and
// the summary says how many were left out and why.
func TestUnansweredAndOpenTasksAreNotRuntimeSamples(t *testing.T) {
	cc := agentdx.SourceClaudeCode
	recs := []rec{
		// Completed.
		prompt("a", 0, cc), turn("a", 100, "r1"), turnEnd("a", 100),
		// Answered, never closed, long ago.
		prompt("b", 0, cc), turn("b", 50, "r1"),
		// Asked, never answered.
		prompt("c", 0, cc),
		// Still running a minute ago.
		prompt("d", 7000, cc), turn("d", 7100, "r1"),
	}
	opts := Options{Now: at(7160)}
	tasks := analyse(t, recs, opts)
	by := map[string]Task{}
	for _, task := range tasks {
		by[task.sessionKey] = task
	}
	if by["a"].Status != StatusCompleted || by["b"].Status != StatusLastObserved || by["d"].Status != StatusOpen {
		t.Errorf("statuses a=%s b=%s d=%s", by["a"].Status, by["b"].Status, by["d"].Status)
	}
	if c := by["c"]; c.ObservedSeconds != nil || c.Unanswered != 1 || c.Status == StatusCompleted {
		t.Errorf("unanswered task = %+v", c)
	}
	for _, ct := range by["c"].Categories {
		if ct.Seconds != nil {
			t.Errorf("unanswered task has %s = %v", ct.Category, *ct.Seconds)
		}
	}
	g := Summarise(tasks)
	if g.Tasks != 4 || g.RuntimeSamples != 2 || g.ExcludedOpen != 1 || g.ExcludedNoTurns != 1 {
		t.Errorf("denominators = %+v", g)
	}
	if g.Observed.N != 2 || *g.Observed.Median != 50 || *g.Observed.P90 != 100 || g.Observed.Method != "nearest-rank" {
		t.Errorf("observed = %+v", g.Observed)
	}
	if g.Completed.N != 1 || *g.Completed.Median != 100 {
		t.Errorf("completed = %+v", g.Completed)
	}
	if g.VerifiedSuccess != nil {
		t.Errorf("verified success claimed from transcripts")
	}
}

// Automatic reviews and substantive work are summarised apart.
func TestGroupsSeparateRoles(t *testing.T) {
	cx := agentdx.SourceCodex
	review := func(s string, start int) []rec {
		return []rec{
			{At: at(start), SessionID: s, Kind: agentdx.KindPrompt, Harness: cx, Role: agentdx.RoleAutoReview},
			{At: at(start + 3), SessionID: s, Kind: agentdx.KindAssistantTurn, Model: "codex-auto-review"},
		}
	}
	recs := append(review("r1", 0), review("r2", 100)...)
	recs = append(recs, review("r3", 200)...)
	recs = append(recs,
		prompt("w", 0, cx), rec{At: at(1500), SessionID: "w", Kind: agentdx.KindAssistantTurn, Model: "model-b"})
	groups := GroupBy(analyse(t, recs, Options{}))
	if len(groups) != 2 {
		t.Fatalf("groups = %+v", groups)
	}
	if groups[0].Role != agentdx.RoleAutoReview || groups[0].Tasks != 3 || *groups[0].Observed.Median != 3 {
		t.Errorf("review group = %+v", groups[0])
	}
	if groups[1].Role != agentdx.RolePrimary || *groups[1].Observed.Median != 1500 {
		t.Errorf("work group = %+v", groups[1])
	}
}

func TestNearestRank(t *testing.T) {
	d := distribution([]float64{5, 1, 4, 2, 3, 6, 7, 8, 9, 10})
	if *d.Median != 5 || *d.P90 != 9 {
		t.Errorf("median %v p90 %v, want 5 and 9", *d.Median, *d.P90)
	}
	if e := distribution(nil); e.Median != nil || e.P90 != nil || e.N != 0 {
		t.Errorf("empty distribution = %+v, want nulls", e)
	}
}
