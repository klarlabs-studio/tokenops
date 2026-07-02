package formatter

import (
	"strings"
	"testing"
)

const pytestRaw = `============================= test session starts ==============================
platform darwin -- Python 3.11.4, pytest-7.4.0, pluggy-1.2.0
rootdir: /Users/dev/project
plugins: cov-4.1.0, anyio-3.7.1
collected 13 items

tests/test_a.py ....F...   [ 50%]
tests/test_b.py ........   [100%]

=========================== FAILURES ===========================
___________________________ test_add ___________________________

    def test_add():
        result = add(1, 2)
>       assert result == 4
E   AssertionError: assert 3 == 4

tests/test_a.py:12: AssertionError
======================= short test summary info ========================
FAILED tests/test_a.py::test_add - AssertionError: assert 3 == 4
======================= 1 failed, 12 passed in 2.34s =======================
`

const pytestAllPassRaw = `============================= test session starts ==============================
platform darwin -- Python 3.11.4, pytest-7.4.0, pluggy-1.2.0
rootdir: /Users/dev/project
collected 15 items

tests/test_a.py ........   [ 53%]
tests/test_b.py .......    [100%]

======================= 15 passed in 1.01s =======================
`

func TestPytest_FailureSurvivesEveryLevel(t *testing.T) {
	p := NewPytest()
	critical := []string{
		"E   AssertionError",
		"1 failed",
		"=== FAILURES ===",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := p.Format([]byte(pytestRaw), level)
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

func TestPytest_BalancedDropsHeader(t *testing.T) {
	p := NewPytest()
	res, _ := p.Format([]byte(pytestRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"platform ", "rootdir:", "collected 13 items"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept header %q:\n%s", noise, compact)
		}
	}
}

func TestPytest_MonotonicReduction(t *testing.T) {
	p := NewPytest()
	for _, raw := range [][]byte{[]byte(pytestRaw), []byte(pytestAllPassRaw)} {
		last := 1 << 30
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, _ := p.Format(raw, level)
			if res.BytesAfter > last {
				t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
			}
			last = res.BytesAfter
		}
	}
}

func TestPytest_AggressiveCollapsesPassingFiles(t *testing.T) {
	p := NewPytest()
	res, _ := p.Format([]byte(pytestAllPassRaw), LossAggressive)
	compact := string(res.Compact)
	if !res.CriticalKept {
		t.Fatal("CriticalKept=false")
	}
	if !strings.Contains(compact, "passing test files") {
		t.Errorf("aggressive should emit a passing-file count:\n%s", compact)
	}
}

func TestPytest_NonPytestFallsBackToGeneric(t *testing.T) {
	p := NewPytest()
	raw := "Traceback (most recent call last):\n  File \"x.py\", line 1\nSyntaxError: bad\n"
	res, ok := p.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
