package taskclass

import "testing"

// A change's class comes from the Conventional Commits type its subject
// opens with, the convention the repositories already follow: a rule an
// operator can read and predict. A subject that names no type is Other,
// kept visible rather than guessed at.
func TestChangeClassReadsTheConventionalType(t *testing.T) {
	for _, tc := range []struct {
		subject string
		want    ChangeClass
	}{
		{"fix(plans): follow the plan the client reports (#648)", ChangeBugfix},
		{"fix: off-by-one", ChangeBugfix},
		{"feat(spend): FOCUS 1.2 export (#667)", ChangeFeature},
		{"feat!: drop the v1 API", ChangeFeature},
		{"refactor(plans)!: name a period's price", ChangeRefactor},
		{"perf: cache the rate card", ChangeRefactor},
		{"test(cli): cover the parity map", ChangeTest},
		{"docs: document the export", ChangeDocs},
		{"chore(deps): bump x", ChangeChore},
		{"ci: pin the action", ChangeChore},
		{"build: go 1.26", ChangeChore},
		{"style: gofmt", ChangeChore},
		{"revert: feat(x): y", ChangeChore},
		{"  FEAT(Spend): upper case still counts", ChangeFeature},
		// Not a type: the word must be followed by a scope or a colon.
		{"fixing the build", ChangeOther},
		{"feature flags for the router", ChangeOther},
		{"Merge pull request #12 from me/branch", ChangeOther},
		{"", ChangeOther},
		{"wip", ChangeOther},
		{"feat(unclosed: scope", ChangeOther},
	} {
		if got := ChangeClassOf(tc.subject); got != tc.want {
			t.Errorf("ChangeClassOf(%q) = %q, want %q", tc.subject, got, tc.want)
		}
	}
}

// Every class a report can name is listed once, in the order a report
// shows them, Other last.
func TestChangeClassesAreOrderedOtherLast(t *testing.T) {
	cs := ChangeClasses()
	if len(cs) != 7 || cs[len(cs)-1] != ChangeOther || cs[0] != ChangeFeature {
		t.Fatalf("ChangeClasses() = %v", cs)
	}
	seen := map[ChangeClass]bool{}
	for _, c := range cs {
		if seen[c] {
			t.Fatalf("%q listed twice", c)
		}
		seen[c] = true
	}
}
