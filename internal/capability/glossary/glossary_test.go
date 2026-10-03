package glossary

import (
	"strings"
	"testing"
)

func TestEveryTermIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, term := range List() {
		if term.Name == "" || term.Area == "" || term.Short == "" || term.What == "" || term.How == "" || term.Read == "" || term.Where == "" {
			t.Errorf("incomplete term %+v", term)
		}
		own := map[string]bool{}
		for _, key := range append([]string{term.Name}, term.Aliases...) {
			k := norm(key)
			if own[k] {
				continue // an alias spelling out the name differently
			}
			own[k] = true
			if seen[k] {
				t.Errorf("%q is claimed by two terms", key)
			}
			seen[k] = true
		}
	}
}

func TestLookup(t *testing.T) {
	for q, want := range map[string]string{
		"wall-clock": "wall-clock", "Wall Clock": "wall-clock", "first-try rate": "first-try",
		"turns (median)": "turns", "api equivalent": "api-equivalent", "5h": "window",
	} {
		got, ok, _ := Lookup(q)
		if !ok || got.Name != want {
			t.Errorf("Lookup(%q) = %q, %v; want %q", q, got.Name, ok, want)
		}
	}
	if _, ok, sugg := Lookup("per"); ok || len(sugg) == 0 {
		t.Errorf("an ambiguous query should suggest, got ok=%v %v", ok, sugg)
	}
}

// Grade bands come from dx's own thresholds, so they cannot drift.
func TestGradesFollowTheThresholds(t *testing.T) {
	turns, _, _ := Lookup("turns")
	if !strings.Contains(turns.Grades, "A up to 5 turns") {
		t.Errorf("turns grades %q", turns.Grades)
	}
	first, _, _ := Lookup("first-try")
	if !strings.Contains(first.Grades, "A at 80% or more") {
		t.Errorf("first-try grades %q", first.Grades)
	}
}
