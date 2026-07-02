package formatter

import (
	"strings"
	"testing"
)

const rspecRaw = `Run options: exclude {:slow=>true}
Randomized with seed 48273

..........

Failures:

  1) User#full_name concatenates first and last name
     Failure/Error: expect(user.full_name).to eq("Jane Doe")

       expected: "Jane Doe"
            got: "Jane"

     # ./spec/user_spec.rb:12:in ` + "`" + `block (3 levels) in <top (required)>'

Finished in 0.4213 seconds (files took 0.128 seconds to load)
42 examples, 1 failure, 0 pending
`

func TestRSpec_FailureSurvivesEveryLevel(t *testing.T) {
	r := NewRSpec()
	critical := []string{
		"1) User#full_name concatenates first and last name",
		"Failure/Error: expect(user.full_name).to eq(\"Jane Doe\")",
		"expected: \"Jane Doe\"",
		"got: \"Jane\"",
		"42 examples, 1 failure, 0 pending",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := r.Format([]byte(rspecRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Fatalf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		for _, c := range critical {
			if !strings.Contains(compact, strings.TrimSpace(c)) {
				t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, c, compact)
			}
		}
	}
}

func TestRSpec_BalancedDropsPasses(t *testing.T) {
	r := NewRSpec()
	res, _ := r.Format([]byte(rspecRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"..........", "Randomized with seed", "Finished in ", "Run options:"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
}

func TestRSpec_MonotonicReduction(t *testing.T) {
	r := NewRSpec()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := r.Format([]byte(rspecRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestRSpec_NonMatchingFallsBackToGeneric(t *testing.T) {
	r := NewRSpec()
	raw := "bundle install\nUsing rake 13.0.6\nBundle complete!\n"
	res, ok := r.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
