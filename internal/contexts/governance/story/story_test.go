package story

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

func at(min int) time.Time {
	return time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute)
}

// unit builds a unit that ran for one minute from the given offset.
func unit(session, prompt string, startMin int) agentdx.Unit {
	return agentdx.Unit{
		SessionID: session,
		Prompt:    prompt,
		Start:     at(startMin),
		End:       at(startMin + 1),
		Turns:     1,
	}
}

func TestGroupSplitsOnIdleGap(t *testing.T) {
	units := []agentdx.Unit{
		unit("s1", "fix the parser", 0),
		unit("s1", "now add a test", 3),   // 2 min after the first ended
		unit("s1", "unrelated thing", 40), // 36 min gap
	}
	tasks := Group(units, Options{})
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
	if got := tasks[0].Instructions(); got != 2 {
		t.Errorf("first task has %d instructions, want 2", got)
	}
	if tasks[1].Boundary != BoundaryIdle {
		t.Errorf("second task boundary = %q, want %q", tasks[1].Boundary, BoundaryIdle)
	}
	if tasks[0].Boundary != BoundarySessionStart {
		t.Errorf("first task boundary = %q, want %q", tasks[0].Boundary, BoundarySessionStart)
	}
}

// The gap is measured from when the agent stopped, not from when the
// instruction was typed: a unit that ran for an hour did not leave the
// operator idle for an hour.
func TestGroupMeasuresGapFromEndOfWork(t *testing.T) {
	long := agentdx.Unit{SessionID: "s1", Prompt: "big refactor", Start: at(0), End: at(60), Turns: 30}
	follow := unit("s1", "now run the tests", 62)
	tasks := Group([]agentdx.Unit{long, follow}, Options{})
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1 — a 60-minute unit followed 2 minutes later is the same work", len(tasks))
	}
}

func TestGroupNeverSpansSessions(t *testing.T) {
	units := []agentdx.Unit{
		unit("s1", "fix the parser", 0),
		unit("s2", "fix the parser", 2), // same wording, new session
	}
	tasks := Group(units, Options{})
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(tasks))
	}
	if tasks[1].Boundary != BoundarySessionStart {
		t.Errorf("boundary = %q, want %q", tasks[1].Boundary, BoundarySessionStart)
	}
}

// Concurrent sessions interleave in wall-clock time. Walking a global
// time order sees the session change at nearly every unit and splits each
// instruction into its own task — the shape this produced on real data
// before units were partitioned by session first.
func TestGroupSurvivesInterleavedConcurrentSessions(t *testing.T) {
	units := []agentdx.Unit{
		unit("s1", "start the refactor", 0),
		unit("s2", "unrelated work", 1),
		unit("s1", "keep going", 2),
		unit("s2", "more unrelated", 3),
		unit("s1", "and finish", 4),
	}
	tasks := Group(units, Options{})
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2 — one per session, not one per instruction", len(tasks))
	}
	counts := map[string]int{}
	for _, tk := range tasks {
		counts[tk.SessionID] = tk.Instructions()
	}
	if counts["s1"] != 3 || counts["s2"] != 2 {
		t.Errorf("instructions per session = %v, want s1:3 s2:2", counts)
	}
}

func TestNegativeIdleGapDisablesSplitting(t *testing.T) {
	units := []agentdx.Unit{
		unit("s1", "one", 0),
		unit("s1", "two", 600),
	}
	if got := len(Group(units, Options{IdleGap: -1})); got != 1 {
		t.Errorf("got %d tasks, want 1 when idle splitting is off", got)
	}
}

func TestTitleQuotesTheFirstInstruction(t *testing.T) {
	u := unit("s1", "fix the parser\nand also the lexer", 0)
	tasks := Group([]agentdx.Unit{u}, Options{})
	if got := tasks[0].Title; got != "fix the parser" {
		t.Errorf("title = %q, want the first line quoted verbatim", got)
	}
}

func TestTitleHandlesMissingText(t *testing.T) {
	// Prompt text is opt-in at extraction. A story built without it must
	// still group and print rather than showing an empty title.
	tasks := Group([]agentdx.Unit{unit("s1", "", 0)}, Options{})
	if got := tasks[0].Title; got != "(no instruction text)" {
		t.Errorf("title = %q, want an explicit placeholder", got)
	}
}

func TestFrictionsReportWhatWentWrong(t *testing.T) {
	u1 := unit("s1", "fix it", 0)
	u1.Rejected = true
	u2 := unit("s1", "no, like this", 2)
	u2.Interrupted = true
	u2.Reworked = true
	tasks := Group([]agentdx.Unit{u1, u2}, Options{})
	fr := tasks[0].Frictions()
	if len(fr) != 3 {
		t.Fatalf("got %d frictions, want 3: %+v", len(fr), fr)
	}
	if fr[0].Kind != "rejected" {
		t.Errorf("first friction = %q, want rejected (it happened first)", fr[0].Kind)
	}
	if tasks[0].Clean() {
		t.Error("Clean() = true on a task with frictions")
	}
}

// A clean run must be distinguishable from an unmeasured one.
func TestCleanTaskReportsNoFriction(t *testing.T) {
	tasks := Group([]agentdx.Unit{unit("s1", "ship it", 0)}, Options{})
	if !tasks[0].Clean() {
		t.Errorf("Clean() = false, want true: %+v", tasks[0].Frictions())
	}
}

func TestRollupsSumAcrossUnits(t *testing.T) {
	u1 := unit("s1", "one", 0)
	u1.Turns, u1.ToolCalls, u1.Tokens = 3, 5, 1000
	u1.Files = []string{"a.go", "b.go"}
	u2 := unit("s1", "two", 2)
	u2.Turns, u2.ToolCalls, u2.Tokens = 2, 1, 500
	u2.Files = []string{"b.go", "c.go"}
	tk := Group([]agentdx.Unit{u1, u2}, Options{})[0]
	if tk.Turns() != 5 || tk.ToolCalls() != 6 || tk.ContextCarried() != 1500 {
		t.Errorf("rollups = turns %d, tools %d, carried %d; want 5, 6, 1500", tk.Turns(), tk.ToolCalls(), tk.ContextCarried())
	}
	// Peak counts the context once; carried double-counts by design.
	if got := tk.PeakContext(); got != 0 {
		t.Errorf("PeakContext() = %d with no per-turn peaks recorded, want 0", got)
	}
	files := tk.Files()
	if len(files) != 3 || files[0] != "a.go" || files[2] != "c.go" {
		t.Errorf("Files() = %v, want a.go, b.go, c.go deduped in first-touch order", files)
	}
}

func TestOrdinalsReadNaturally(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{1, "1st"}, {2, "2nd"}, {3, "3rd"}, {4, "4th"},
		{11, "11th"}, {12, "12th"}, {13, "13th"}, {21, "21st"}, {112, "112th"},
	} {
		if got := ordinal(tc.n); got != tc.want {
			t.Errorf("ordinal(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
