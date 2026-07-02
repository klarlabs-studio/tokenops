package formatter

import (
	"strings"
	"testing"
)

const goTestRaw = `=== RUN   TestAlpha
--- PASS: TestAlpha (0.00s)
=== RUN   TestBeta
=== PAUSE TestBeta
=== CONT  TestBeta
--- FAIL: TestBeta (0.01s)
    beta_test.go:42: got 3, want 4
=== RUN   TestGamma
--- PASS: TestGamma (0.00s)
PASS
FAIL	github.com/example/pkg	0.123s
ok  	github.com/example/other	0.045s
ok  	github.com/example/third	(cached)
`

func TestGoTest_FailureSurvivesEveryLevel(t *testing.T) {
	g := NewGoTest()
	critical := []string{
		"--- FAIL: TestBeta (0.01s)",
		"beta_test.go:42: got 3, want 4",
		"FAIL	github.com/example/pkg	0.123s",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := g.Format([]byte(goTestRaw), level)
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

func TestGoTest_BalancedDropsScaffolding(t *testing.T) {
	g := NewGoTest()
	res, _ := g.Format([]byte(goTestRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"=== RUN", "=== PAUSE", "=== CONT", "--- PASS"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept scaffolding %q:\n%s", noise, compact)
		}
	}
}

func TestGoTest_AggressiveCollapsesPassingPackages(t *testing.T) {
	g := NewGoTest()
	res, _ := g.Format([]byte(goTestRaw), LossAggressive)
	compact := string(res.Compact)
	if strings.Contains(compact, "github.com/example/other") {
		t.Errorf("aggressive should collapse passing packages:\n%s", compact)
	}
	if !strings.Contains(compact, "passing packages") {
		t.Errorf("aggressive should emit a passing-package count:\n%s", compact)
	}
	// The failing package line must still be present.
	if !strings.Contains(compact, "FAIL\tgithub.com/example/pkg") {
		t.Errorf("aggressive dropped the FAIL package line:\n%s", compact)
	}
}

func TestGoTest_MonotonicReduction(t *testing.T) {
	g := NewGoTest()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := g.Format([]byte(goTestRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestGoTest_NonTestFallsBackToGeneric(t *testing.T) {
	g := NewGoTest()
	raw := "go: downloading example.com/x v1.2.3\ngo: downloading example.com/y v0.1.0\n"
	res, ok := g.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestGoTest_CompileErrorIsCritical(t *testing.T) {
	g := NewGoTest()
	if !g.CriticalLine("./internal/cli/fmt.go:42:6: undefined: foo") {
		t.Error("go compile error should be critical")
	}
	if g.CriticalLine("some random log line") {
		t.Error("plain line should not be critical")
	}
}
