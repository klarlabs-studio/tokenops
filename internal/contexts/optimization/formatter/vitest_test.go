package formatter

import (
	"strings"
	"testing"
)

const vitestRaw = ` ✓ src/util/math.test.ts (5 tests) 12ms
 ✓ src/util/string.test.ts (8 tests) 9ms
 ❯ src/api/users.test.ts (3 tests | 1 failed) 21ms
   × returns the created user
 ✓ src/api/health.test.ts (2 tests) 4ms

⎯⎯⎯⎯⎯⎯⎯ Failed Tests 1 ⎯⎯⎯⎯⎯⎯⎯

FAIL src/api/users.test.ts > returns the created user
AssertionError: expected { id: 1, name: 'Bob' } to deeply equal { id: 1, name: 'Ada' }
- Expected
+ Received

 Test Files  1 failed | 3 passed (4)
      Tests  1 failed | 40 passed (41)
   Start at  10:00:00
   Duration  1.23s
`

func TestVitest_FailureSurvivesEveryLevel(t *testing.T) {
	v := NewVitest()
	critical := []string{
		"❯ src/api/users.test.ts (3 tests | 1 failed) 21ms",
		"× returns the created user",
		"FAIL src/api/users.test.ts > returns the created user",
		"AssertionError: expected { id: 1, name: 'Bob' }",
		"Tests  1 failed | 40 passed (41)",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := v.Format([]byte(vitestRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Fatalf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		for _, c := range critical {
			if !strings.Contains(compact, c) {
				t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, c, compact)
			}
		}
	}
}

func TestVitest_BalancedDropsPasses(t *testing.T) {
	v := NewVitest()
	res, _ := v.Format([]byte(vitestRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"src/util/math.test.ts", "src/util/string.test.ts", "src/api/health.test.ts"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept passing file %q:\n%s", noise, compact)
		}
	}
	// The failing file marker must survive.
	if !strings.Contains(compact, "❯ src/api/users.test.ts") {
		t.Errorf("balanced dropped the failing file marker:\n%s", compact)
	}
}

func TestVitest_MonotonicReduction(t *testing.T) {
	v := NewVitest()
	last := 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := v.Format([]byte(vitestRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestVitest_NonMatchingFallsBackToGeneric(t *testing.T) {
	v := NewVitest()
	raw := "npm warn deprecated foo@1.0.0: use bar\nadded 120 packages in 3s\n"
	res, ok := v.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
