package agentdx

import "testing"

func reworkOf(t *testing.T, recs []Record) int {
	t.Helper()
	units := Units(recs)
	if len(units) != 1 {
		t.Fatalf("want 1 unit, got %d", len(units))
	}
	return units[0].ReworkEdits
}

// Making a multi-part change to one file is how editing works, not waste.
// Two Edit calls to different sections of one file inside one instruction
// were counted as "revisited a file this unit had already edited" — and that
// metric drives the overall grade, so ordinary work graded as friction.
func TestConsecutiveEditsToOneFileAreOneEpisode(t *testing.T) {
	recs := []Record{
		prompt(0, "s"), turn(1, "s", 0),
		tool(2, "s", "Edit", "/a.go"),
		tool(3, "s", "Edit", "/a.go"),
		tool(4, "s", "Edit", "/a.go"),
	}
	if got := reworkOf(t, recs); got != 0 {
		t.Fatalf("ReworkEdits = %d, want 0 — one editing episode is not rework", got)
	}
}

// Reads and other tool calls between edits do not end the episode: looking
// at the file you are editing is part of editing it.
func TestInterveningNonEditsDoNotEndTheEpisode(t *testing.T) {
	recs := []Record{
		prompt(0, "s"), turn(1, "s", 0),
		tool(2, "s", "Edit", "/a.go"),
		tool(3, "s", "Read", "/a.go"),
		tool(4, "s", "Bash", ""),
		tool(5, "s", "Edit", "/a.go"),
	}
	if got := reworkOf(t, recs); got != 0 {
		t.Fatalf("ReworkEdits = %d, want 0", got)
	}
}

// Coming back to a file after moving on to another one is the thing the
// metric is named for, and it still counts.
func TestReturningToAFileAfterEditingAnotherIsRework(t *testing.T) {
	recs := []Record{
		prompt(0, "s"), turn(1, "s", 0),
		tool(2, "s", "Edit", "/a.go"),
		tool(3, "s", "Edit", "/b.go"),
		tool(4, "s", "Edit", "/a.go"),
	}
	if got := reworkOf(t, recs); got != 1 {
		t.Fatalf("ReworkEdits = %d, want 1 — /a.go was returned to", got)
	}
}

// Each return counts, so a file bounced between twice scores twice.
func TestEachReturnCounts(t *testing.T) {
	recs := []Record{
		prompt(0, "s"), turn(1, "s", 0),
		tool(2, "s", "Edit", "/a.go"),
		tool(3, "s", "Edit", "/b.go"),
		tool(4, "s", "Edit", "/a.go"),
		tool(5, "s", "Edit", "/b.go"),
		tool(6, "s", "Edit", "/a.go"),
	}
	if got := reworkOf(t, recs); got != 3 {
		t.Fatalf("ReworkEdits = %d, want 3", got)
	}
}

// Edits never collapse across instructions: a new prompt starts a new unit,
// so touching the same file again is a fresh episode, not a return.
func TestANewInstructionStartsFresh(t *testing.T) {
	units := Units([]Record{
		prompt(0, "s"), turn(1, "s", 0), tool(2, "s", "Edit", "/a.go"),
		prompt(10, "s"), turn(11, "s", 0), tool(12, "s", "Edit", "/a.go"),
	})
	if len(units) != 2 {
		t.Fatalf("want 2 units, got %d", len(units))
	}
	for i, u := range units {
		if u.ReworkEdits != 0 {
			t.Errorf("unit %d: ReworkEdits = %d, want 0", i, u.ReworkEdits)
		}
	}
}
