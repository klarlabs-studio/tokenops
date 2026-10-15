package waste

import (
	"testing"
)

func TestAttributeNeverGivesATokenToTwoClasses(t *testing.T) {
	budget := []int64{1_000, 500, 0, 2_000}
	claims := []Claim{
		// Claims arrive in any order; the attribution order decides.
		{Class: ClassAbandoned, Turns: []int{0, 1, 2, 3}, Tokens: 10_000},
		{Class: ClassReread, Turns: []int{0}, Tokens: 800},
		{Class: ClassOversized, Turns: []int{0, 3}, Tokens: 1_500},
		{Class: ClassCacheRebuild, Turns: []int{1}, Tokens: 900},
		{Class: ClassRetryLoop, Turns: []int{2}, Tokens: 50},
	}
	got := Attribute(budget, claims)
	if len(got) != len(budget) {
		t.Fatalf("got %d turns, want %d", len(got), len(budget))
	}
	var total int64
	for i, byClass := range got {
		var turn int64
		for _, n := range byClass {
			if n < 0 {
				t.Fatalf("turn %d: negative attribution %v", i, byClass)
			}
			turn += n
		}
		if turn > budget[i] {
			t.Errorf("turn %d: %d tokens attributed of %d", i, turn, budget[i])
		}
		total += turn
	}
	// Every token is claimed by the abandoned session, so all of them are
	// attributed, each exactly once.
	if total != 3_500 {
		t.Errorf("attributed %d, want 3500", total)
	}
	if got[0][ClassReread] != 800 {
		t.Errorf("reread = %d, want its whole claim", got[0][ClassReread])
	}
	if got[1][ClassCacheRebuild] != 500 {
		t.Errorf("cache rebuild = %d, want capped at the turn's 500", got[1][ClassCacheRebuild])
	}
	if got[2][ClassRetryLoop] != 0 {
		t.Errorf("a turn with no tokens gave %d", got[2][ClassRetryLoop])
	}
	// Oversized drew what the re-read left on turn 0 (200) in proportion
	// with turn 3 (2000) and took the rest from turn 3.
	if o := got[0][ClassOversized] + got[3][ClassOversized]; o != 1_500 {
		t.Errorf("oversized = %d, want 1500", o)
	}
	if got[0][ClassOversized] > 200 {
		t.Errorf("oversized took %d from turn 0, which had 200 left", got[0][ClassOversized])
	}
	if a := got[0][ClassAbandoned] + got[1][ClassAbandoned] + got[3][ClassAbandoned]; a != 3_500-800-500-1_500 {
		t.Errorf("abandoned = %d, want what was left", a)
	}
}

func TestAttributeSpreadsAClaimInProportion(t *testing.T) {
	got := Attribute([]int64{300, 100}, []Claim{{Class: ClassRetryLoop, Turns: []int{0, 1}, Tokens: 200}})
	if got[0][ClassRetryLoop] != 150 || got[1][ClassRetryLoop] != 50 {
		t.Errorf("got %v, want 150 and 50", got)
	}
}

func TestAttributeIgnoresTurnsOutOfRange(t *testing.T) {
	got := Attribute([]int64{100}, []Claim{{Class: ClassReread, Turns: []int{-1, 0, 7}, Tokens: 40}})
	if got[0][ClassReread] != 40 {
		t.Errorf("got %v", got)
	}
}

func TestClassesCoverTheTaxonomyOnce(t *testing.T) {
	seen := map[Class]bool{}
	for _, c := range Classes() {
		if seen[c] {
			t.Fatalf("%s listed twice", c)
		}
		seen[c] = true
		if ClassName(c) == "" || ClassName(c) == string(c) {
			t.Errorf("%s has no name", c)
		}
	}
	if len(seen) != 6 {
		t.Errorf("%d classes, want 6", len(seen))
	}
}
