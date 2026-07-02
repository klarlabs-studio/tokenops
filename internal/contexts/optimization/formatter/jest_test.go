package formatter

import (
	"strings"
	"testing"
)

const jestRaw = `PASS src/util/math.test.js
PASS src/util/string.test.js
  console.log
    debug: connecting to fixture db
      at Object.<anonymous> (src/util/string.test.js:4:11)
FAIL src/api/users.test.js
  ● Users API › returns the created user

    expect(received).toEqual(expected)

    Expected: {"id": 1, "name": "Ada"}
    Received: {"id": 1, "name": "Bob"}

      at Object.<anonymous> (src/api/users.test.js:22:24)
PASS src/api/health.test.js
Test Suites: 1 failed, 3 passed, 4 total
Tests:       2 failed, 40 passed, 42 total
Snapshots:   0 total
Time:        3.204 s
`

func TestJest_FailureSurvivesEveryLevel(t *testing.T) {
	j := NewJest()
	critical := []string{
		"FAIL src/api/users.test.js",
		"● Users API › returns the created user",
		"Expected: {\"id\": 1, \"name\": \"Ada\"}",
		"Received: {\"id\": 1, \"name\": \"Bob\"}",
		"Tests:       2 failed, 40 passed, 42 total",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := j.Format([]byte(jestRaw), level)
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

func TestJest_BalancedDropsPasses(t *testing.T) {
	j := NewJest()
	res, _ := j.Format([]byte(jestRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"PASS src/util/math.test.js", "PASS src/api/health.test.js", "console.log"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept passing noise %q:\n%s", noise, compact)
		}
	}
	// The failing suite line must survive.
	if !strings.Contains(compact, "FAIL src/api/users.test.js") {
		t.Errorf("balanced dropped the FAIL suite line:\n%s", compact)
	}
}

func TestJest_MonotonicReduction(t *testing.T) {
	j := NewJest()
	last := 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := j.Format([]byte(jestRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestJest_NonMatchingFallsBackToGeneric(t *testing.T) {
	j := NewJest()
	raw := "yarn install v1.22.19\n[1/4] Resolving packages...\n[2/4] Fetching packages...\n"
	res, ok := j.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
