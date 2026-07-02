package formatter

import (
	"strings"
	"testing"
)

const ninjaRaw = `[1/120] Compiling foo.cpp
[2/120] Compiling bar.cpp
/src/bar.cpp:14:9: warning: unused variable 'y' [-Wunused-variable]
[3/120] Compiling baz.cpp
/src/baz.cpp:20:3: error: 'x' was not declared in this scope
FAILED: obj/baz.o
[4/120] Linking libapp.so
ninja: build stopped: subcommand failed.
`

const ninjaSuccessRaw = `[1/3] Compiling foo.cpp
[2/3] Compiling bar.cpp
[3/3] Linking app
`

func TestNinja_CriticalSurvivesEveryLevel(t *testing.T) {
	n := NewNinja()
	critical := []string{
		"/src/baz.cpp:20:3: error: 'x' was not declared in this scope",
		"FAILED: obj/baz.o",
		"ninja: build stopped: subcommand failed.",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := n.Format([]byte(ninjaRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Fatalf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		for _, cl := range critical {
			if !strings.Contains(compact, strings.TrimSpace(cl)) {
				t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, cl, compact)
			}
		}
	}
}

func TestNinja_BalancedDropsProgress(t *testing.T) {
	n := NewNinja()
	res, _ := n.Format([]byte(ninjaRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"[1/120] Compiling", "[4/120] Linking"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept progress noise %q:\n%s", noise, compact)
		}
	}
	// Advisory warnings survive at Balanced, are dropped at Aggressive.
	if !strings.Contains(compact, ": warning:") {
		t.Errorf("balanced should keep advisory warnings:\n%s", compact)
	}
	aggressive, _ := n.Format([]byte(ninjaRaw), LossAggressive)
	if strings.Contains(string(aggressive.Compact), ": warning:") {
		t.Errorf("aggressive should drop advisory warnings:\n%s", aggressive.Compact)
	}
}

func TestNinja_MonotonicReduction(t *testing.T) {
	n := NewNinja()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := n.Format([]byte(ninjaRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestNinja_NonMatchingFallsBackToGeneric(t *testing.T) {
	n := NewNinja()
	raw := "deploying app v1.2.3\nall done\n"
	res, ok := n.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestNinja_CleanRunCollapses(t *testing.T) {
	n := NewNinja()
	res, ok := n.Format([]byte(ninjaSuccessRaw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !res.CriticalKept {
		t.Fatal("CriticalKept=false on a clean run")
	}
	compact := string(res.Compact)
	for _, noise := range []string{"[1/3] Compiling", "[3/3] Linking"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced should drop progress chatter %q:\n%s", noise, compact)
		}
	}
}

func TestNinja_ErrorsAreCritical(t *testing.T) {
	n := NewNinja()
	crit := []string{
		"/src/baz.cpp:20:3: error: 'x' was not declared in this scope",
		"main.cpp:3:1: fatal error: iostream: No such file or directory",
		"FAILED: obj/baz.o",
		"ninja: build stopped: subcommand failed.",
		"ninja: error: '../foo.cpp', needed by 'obj/foo.o', missing",
	}
	for _, cl := range crit {
		if !n.CriticalLine(cl) {
			t.Errorf("expected critical: %q", cl)
		}
	}
	noncrit := []string{
		"/src/bar.cpp:14:9: warning: unused variable 'y'",
		"[1/120] Compiling foo.cpp",
		"[4/120] Linking libapp.so",
	}
	for _, cl := range noncrit {
		if n.CriticalLine(cl) {
			t.Errorf("expected non-critical: %q", cl)
		}
	}
}
