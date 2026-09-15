package cli

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
)

func renderTask(title string, start time.Time, mins int, units ...agentdx.Unit) story.Task {
	if len(units) == 0 {
		units = []agentdx.Unit{{Prompt: title, Start: start, Turns: 3, ToolCalls: 4}}
	}
	return story.Task{
		Title:    title,
		Start:    start,
		End:      start.Add(time.Duration(mins) * time.Minute),
		Units:    units,
		Boundary: story.BoundarySessionStart,
	}
}

func render(t *testing.T, f func(w *strings.Builder)) string {
	t.Helper()
	var b strings.Builder
	f(&b)
	return b.String()
}

// The report goes to someone who was not there. It carries the facts a
// transcript actually holds — when, how long, at what volume, against
// which files — and it says plainly what those figures are not.
func TestReportCarriesTheEvidenceAndItsLimits(t *testing.T) {
	start := time.Date(2026, 9, 15, 9, 30, 0, 0, time.Local)
	tasks := []story.Task{
		renderTask("extend the continuation lexicon", start, 52,
			agentdx.Unit{Prompt: "extend the continuation lexicon", Start: start, Turns: 41, ToolCalls: 118,
				Files: []string{"/tmp/continuation.go"}}),
	}
	out := render(t, func(b *strings.Builder) { writeStoryReport(b, tasks, "the last week") })

	for _, want := range []string{
		"extend the continuation lexicon", // the instruction, quoted
		"the last week",
		"52m",
		"41 turns",
		"118 tool calls",
		"Tuesday, 15 September 2026",
		"not a timesheet", // what the figure is not
		"Nothing here asserts that the work is correct", // no claim about correctness
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
}

// Defensibility over candour. "You told it the third answer was wrong" is
// candour aimed at the operator; in front of a client it turns an account
// of work into an apology for it.
func TestReportOmitsTheFrictionNarrative(t *testing.T) {
	start := time.Date(2026, 9, 15, 9, 30, 0, 0, time.Local)
	tasks := []story.Task{
		renderTask("fix the parser", start, 20,
			agentdx.Unit{Prompt: "fix the parser", Start: start, Turns: 9, Rejected: true, Reworked: true}),
	}
	out := render(t, func(b *strings.Builder) { writeStoryReport(b, tasks, "the last week") })
	for _, unwanted := range []string{"answer was wrong", "edited the same file twice", "sideways"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("report contains the operator's candour: %q\n%s", unwanted, out)
		}
	}
}

// The handover is ordered by what needs attention, not by time: work that
// ended on friction is where the loose ends are.
func TestHandoffLeadsWithUnfinishedWork(t *testing.T) {
	start := time.Date(2026, 9, 15, 9, 0, 0, 0, time.Local)
	tasks := []story.Task{
		renderTask("ran fine", start, 10),
		renderTask("went sideways", start.Add(time.Hour), 30,
			agentdx.Unit{Prompt: "went sideways", Start: start.Add(time.Hour), Turns: 12, Interrupted: true}),
	}
	out := render(t, func(b *strings.Builder) { writeStoryHandoff(b, tasks, "the last week") })

	friction := strings.Index(out, "went sideways")
	clean := strings.Index(out, "ran fine")
	if friction < 0 || clean < 0 {
		t.Fatalf("handover is missing a task:\n%s", out)
	}
	if friction > clean {
		t.Errorf("clean work came before the loose ends:\n%s", out)
	}
	if !strings.Contains(out, "stopped the agent") {
		t.Errorf("the handover hides how the work ended:\n%s", out)
	}
}

// A transcript records what was attempted. Whether the code works is a
// question only the tests answer, and a handover that implies otherwise
// is worse than no handover.
func TestHandoffNeverClaimsWorkLanded(t *testing.T) {
	start := time.Date(2026, 9, 15, 9, 0, 0, 0, time.Local)
	out := render(t, func(b *strings.Builder) {
		writeStoryHandoff(b, []story.Task{renderTask("ran fine", start, 10)}, "the last week")
	})
	if !strings.Contains(out, "not whether it works") {
		t.Errorf("the handover does not disclaim correctness:\n%s", out)
	}
	for _, unwanted := range []string{"landed", "done", "complete"} {
		if strings.Contains(strings.ToLower(out), unwanted) {
			t.Errorf("the handover claims %q about work it cannot verify:\n%s", unwanted, out)
		}
	}
}

// Neither rendering leaves this machine's home directory in a document
// meant for somebody else.
func TestRenderingsDoNotLeakTheHomePath(t *testing.T) {
	if !strings.HasPrefix(displayPath("/definitely/not/home/a.go"), "/definitely") {
		t.Error("a path outside the home directory was rewritten")
	}
	got := displayPath(strings.Repeat("/averylongdirectoryname", 5) + "/thefile.go")
	if !strings.HasPrefix(got, "…/") {
		t.Errorf("displayPath(%q) did not elide from the left", got)
	}
	if !strings.HasSuffix(got, "/thefile.go") {
		t.Errorf("displayPath dropped the part that identifies the file: %q", got)
	}
}

// A typo in --for must not quietly produce the operator's own rendering.
// That is how someone emails a client the candid one.
func TestUnknownAudienceIsRejected(t *testing.T) {
	if err := validateAudience("reprot"); err == nil {
		t.Error("validateAudience(\"reprot\") = nil; a typo silently fell back to the default")
	}
	for _, a := range []string{"", "me", "agent", "REPORT", " handoff "} {
		if err := validateAudience(a); err != nil {
			t.Errorf("validateAudience(%q) = %v, want nil", a, err)
		}
	}
	// --json predates --for and keeps meaning the machine rendering.
	if got := resolveAudience("report", true); got != audienceAgent {
		t.Errorf("resolveAudience with --json = %q, want %q", got, audienceAgent)
	}
}
