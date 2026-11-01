package commits

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
)

var t0 = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

func turn(session string, at time.Duration, usd float64) analytics.SessionTurn {
	return analytics.SessionTurn{SessionID: session, At: t0.Add(at), Tokens: 1000, APIEquivalentUSD: usd, Priced: true}
}

// Each turn goes to the next commit in its repository within a day; the
// rest is kept apart, by why.
func TestAttribute(t *testing.T) {
	dirs := map[string]sessiondirs.Dir{
		"a": {CWD: "/w/app/sub"}, "b": {CWD: "/w/app"}, "c": {CWD: "/tmp/scratch"},
	}
	rootOf := map[string]string{"/w/app/sub": "/w/app", "/w/app": "/w/app", "/tmp/scratch": ""}
	commitsOf := map[string][]gitlog.Commit{"/w/app": {
		{SHA: "1111111111111111", At: t0.Add(time.Hour), Subject: "first"},
		{SHA: "2222222222222222", At: t0.Add(3 * time.Hour), Subject: "second"},
		{SHA: "3333333333333333", At: t0.Add(80 * time.Hour), Subject: "much later"},
	}}
	turns := []analytics.SessionTurn{
		turn("a", 0, 2), turn("b", 30*time.Minute, 1), // → first (two sessions)
		turn("a", 2*time.Hour, 4),   // → second
		turn("a", 4*time.Hour, 8),   // next commit 76 h away → no commit within a day
		turn("b", 90*time.Hour, 16), // after the last commit → uncommitted
		turn("c", time.Hour, 32),    // not a repository
		turn("z", time.Hour, 64),    // unknown session
	}
	r := Attribute(t0, turns, dirs, rootOf, commitsOf)
	if len(r.Commits) != 2 {
		t.Fatalf("commits = %+v", r.Commits)
	}
	second, first := r.Commits[0], r.Commits[1]
	if first.SHA != "111111111111" || first.APIEquivalentUSD != 3 || first.Sessions != 2 || first.Turns != 2 || first.WorkMinutes != 60 {
		t.Errorf("first = %+v", first)
	}
	if second.Subject != "second" || second.APIEquivalentUSD != 4 || second.Repo != "app" {
		t.Errorf("second = %+v", second)
	}
	if r.NoCommitWithinADay.APIEquivalentUSD != 8 || r.Uncommitted.APIEquivalentUSD != 16 ||
		r.OutsideARepository.APIEquivalentUSD != 32 || r.UnknownDirectory.APIEquivalentUSD != 64 {
		t.Errorf("unplaced = %+v %+v %+v %+v", r.NoCommitWithinADay, r.Uncommitted, r.OutsideARepository, r.UnknownDirectory)
	}
	if r.MedianUSD != 3.5 || r.MeanUSD != 3.5 {
		t.Errorf("median %v mean %v", r.MedianUSD, r.MeanUSD)
	}
	if want := 7.0 / 127; r.AttributedShare < want-1e-9 || r.AttributedShare > want+1e-9 {
		t.Errorf("attributed share = %v, want %v", r.AttributedShare, want)
	}
	if w := r.WithoutSubjects(); w.Commits[0].Subject != "" || !w.SubjectsWithheld || r.Commits[0].Subject == "" {
		t.Error("WithoutSubjects leaked or changed the original")
	}
}

// Place answers, for one turn, the join Attribute makes: so another
// report (the waste taxonomy's abandoned sessions) reads the same join.
func TestPlace(t *testing.T) {
	dirs := map[string]sessiondirs.Dir{"a": {CWD: "/w/app"}, "c": {CWD: "/tmp"}}
	rootOf := map[string]string{"/w/app": "/w/app", "/tmp": ""}
	commitsOf := map[string][]gitlog.Commit{"/w/app": {{SHA: "1", At: t0.Add(time.Hour)}, {SHA: "2", At: t0.Add(80 * time.Hour)}}}
	for _, c := range []struct {
		turn analytics.SessionTurn
		want Placement
	}{
		{turn("a", 0, 1), OnCommit},
		{turn("a", 2*time.Hour, 1), NoCommitWithinADay},
		{turn("a", 90*time.Hour, 1), Uncommitted},
		{turn("c", 0, 1), OutsideARepository},
		{turn("z", 0, 1), UnknownDirectory},
	} {
		if got, _ := Place(c.turn, dirs, rootOf, commitsOf); got != c.want {
			t.Errorf("%s at %s: %v, want %v", c.turn.SessionID, c.turn.At, got, c.want)
		}
	}
	if _, c := Place(turn("a", 0, 1), dirs, rootOf, commitsOf); c.SHA != "1" {
		t.Errorf("commit = %+v", c)
	}
}

// Unpriced turns count as turns and are flagged; the money leaves them out.
func TestUnpricedTurnsAreFlagged(t *testing.T) {
	u := turn("a", 0, 0)
	u.Priced = false
	r := Attribute(t0, []analytics.SessionTurn{u},
		map[string]sessiondirs.Dir{"a": {CWD: "/w/app"}}, map[string]string{"/w/app": "/w/app"},
		map[string][]gitlog.Commit{"/w/app": {{SHA: "abc", At: t0.Add(time.Minute)}}})
	if len(r.Commits) != 1 || r.Commits[0].UnpricedTurns != 1 || r.Commits[0].SHA != "abc" {
		t.Errorf("commits = %+v", r.Commits)
	}
}

func TestNewest(t *testing.T) {
	r := Report{Commits: []CommitCost{{SHA: "c"}, {SHA: "b"}, {SHA: "a"}}, CommitsTotal: 3}
	if got := r.Newest(2); len(got.Commits) != 2 || got.CommitsTotal != 3 || got.Commits[0].SHA != "c" {
		t.Errorf("Newest(2) = %+v", got)
	}
	if got := r.Newest(0); len(got.Commits) != 3 {
		t.Error("Newest(0) trimmed")
	}
}

// A commit undone by a revert within RevertWindow is marked reverted; one
// reverted later, or not at all, is not.
func TestAttributeMarksRevertedCommits(t *testing.T) {
	dirs := map[string]sessiondirs.Dir{"a": {CWD: "/w/app"}}
	rootOf := map[string]string{"/w/app": "/w/app"}
	commitsOf := map[string][]gitlog.Commit{"/w/app": {
		{SHA: "1111111111111111", At: t0.Add(time.Hour), Subject: "feat: undone"},
		{SHA: "2222222222222222", At: t0.Add(2 * time.Hour), Subject: "feat: kept"},
		{SHA: "3333333333333333", At: t0.Add(3 * time.Hour), Subject: "feat: undone too late"},
		{SHA: "4444444444444444", At: t0.Add(5 * time.Hour), Subject: `Revert "feat: undone"`, Reverts: "1111111"},
		{SHA: "5555555555555555", At: t0.Add(3*time.Hour + RevertWindow + time.Hour), Subject: `Revert "feat: undone too late"`, Reverts: "3333333333333333"},
	}}
	turns := []analytics.SessionTurn{turn("a", 0, 1), turn("a", 90*time.Minute, 2), turn("a", 150*time.Minute, 4)}
	r := Attribute(t0, turns, dirs, rootOf, commitsOf)
	reverted := map[string]bool{}
	for _, c := range r.Commits {
		reverted[c.Subject] = c.Reverted
	}
	if !reverted["feat: undone"] || reverted["feat: kept"] || reverted["feat: undone too late"] {
		t.Errorf("reverted = %v", reverted)
	}
}
