package formatter

import (
	"strings"
	"testing"
)

const makeRaw = `make[1]: Entering directory '/build'
gcc -c -Wall -O2 foo.c -o foo.o
gcc -c -Wall -O2 bar.c -o bar.o
gcc -c -Wall -O2 baz.c -o baz.o
foo.c:8:3: warning: unused variable 'y' [-Wunused-variable]
bar.c:14:5: error: 'x' undeclared (first use in this function)
make[1]: Leaving directory '/build'
make: *** [Makefile:20: bar.o] Error 1
`

const makeSuccessRaw = `make[1]: Entering directory '/build'
make[1]: Nothing to be done for 'all'.
make[1]: Leaving directory '/build'
`

func TestMake_CriticalSurvivesEveryLevel(t *testing.T) {
	m := NewMake()
	critical := []string{
		"make: *** [Makefile:20: bar.o] Error 1",
		"bar.c:14:5: error: 'x' undeclared (first use in this function)",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := m.Format([]byte(makeRaw), level)
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

func TestMake_BalancedDropsRecipeEcho(t *testing.T) {
	m := NewMake()
	res, _ := m.Format([]byte(makeRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"make[1]", "gcc -c -Wall"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept recipe/directory noise %q:\n%s", noise, compact)
		}
	}
}

func TestMake_AggressiveDropsWarnings(t *testing.T) {
	m := NewMake()
	balanced, _ := m.Format([]byte(makeRaw), LossBalanced)
	if !strings.Contains(string(balanced.Compact), ": warning:") {
		t.Errorf("balanced should keep advisory warnings:\n%s", balanced.Compact)
	}
	aggressive, _ := m.Format([]byte(makeRaw), LossAggressive)
	if strings.Contains(string(aggressive.Compact), ": warning:") {
		t.Errorf("aggressive should drop advisory warnings:\n%s", aggressive.Compact)
	}
}

func TestMake_MonotonicReduction(t *testing.T) {
	m := NewMake()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := m.Format([]byte(makeRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestMake_NonMakeFallsBackToGeneric(t *testing.T) {
	m := NewMake()
	raw := "deploying app v1.2.3\nall done\n"
	res, ok := m.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestMake_SuccessRunCollapses(t *testing.T) {
	m := NewMake()
	res, ok := m.Format([]byte(makeSuccessRaw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !res.CriticalKept {
		t.Fatal("CriticalKept=false on a clean run")
	}
	if strings.Contains(string(res.Compact), "make[1]") {
		t.Errorf("balanced should drop directory chatter:\n%s", res.Compact)
	}
}

func TestMake_CompilerAndMakeErrorsAreCritical(t *testing.T) {
	m := NewMake()
	crit := []string{
		"make: *** [target] Error 1",
		"*** No rule to make target 'x'.  Stop.",
		"bar.c:14:5: error: 'x' undeclared",
		"main.c:3:1: fatal error: stdio.h: No such file or directory",
		"undefined reference to `foo'",
		"ld: symbol(s) not found for architecture arm64",
	}
	for _, c := range crit {
		if !m.CriticalLine(c) {
			t.Errorf("expected critical: %q", c)
		}
	}
	noncrit := []string{
		"foo.c:8:3: warning: unused variable 'y'",
		"gcc -c -Wall -O2 foo.c -o foo.o",
		"make[1]: Entering directory '/build'",
	}
	for _, c := range noncrit {
		if m.CriticalLine(c) {
			t.Errorf("expected non-critical: %q", c)
		}
	}
}
