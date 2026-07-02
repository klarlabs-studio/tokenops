package formatter

import (
	"strings"
	"testing"
)

const mixFailRaw = `==> myapp
Compiling 24 files (.ex)
Generated myapp app
warning: variable "x" is unused
  lib/foo.ex:10
..

  1) test the thing works (MyAppTest)
     test/my_app_test.exs:8
     Assertion with == failed
     ** (RuntimeError) boom

Finished in 0.1 seconds
5 tests, 1 failure
`

const mixOkRaw = `==> myapp
Compiling 24 files (.ex)
Generated myapp app
.....

Finished in 0.2 seconds
5 tests, 0 failures
`

func TestMix_CriticalSurvivesEveryLevel(t *testing.T) {
	m := NewMix()
	critical := []string{
		"1) test the thing works (MyAppTest)",
		"** (RuntimeError) boom",
		"5 tests, 1 failure",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := m.Format([]byte(mixFailRaw), level)
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
	// The summary line survives on the passing fixture at every level.
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := m.Format([]byte(mixOkRaw), level)
		if !strings.Contains(string(res.Compact), "5 tests, 0 failures") {
			t.Errorf("level=%s dropped the summary line:\n%s", level, res.Compact)
		}
	}
}

func TestMix_BalancedDropsNoise(t *testing.T) {
	m := NewMix()
	res, _ := m.Format([]byte(mixFailRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"==> myapp",
		"Compiling 24 files",
		"Generated myapp app",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept progress noise %q:\n%s", noise, compact)
		}
	}
	// Test failure, error, summary, and the warning survive at Balanced.
	if !strings.Contains(compact, "1) test the thing works (MyAppTest)") {
		t.Errorf("balanced dropped the test-failure header:\n%s", compact)
	}
	if !strings.Contains(compact, `warning: variable "x" is unused`) {
		t.Errorf("balanced dropped the warning line:\n%s", compact)
	}

	// Aggressive additionally drops the warning advisory and its location line.
	resAgg, _ := m.Format([]byte(mixFailRaw), LossAggressive)
	aggCompact := string(resAgg.Compact)
	if strings.Contains(aggCompact, `warning: variable "x" is unused`) {
		t.Errorf("aggressive kept the warning line:\n%s", aggCompact)
	}
	if !strings.Contains(aggCompact, "** (RuntimeError) boom") {
		t.Errorf("aggressive dropped an error line:\n%s", aggCompact)
	}
	if !strings.Contains(aggCompact, "5 tests, 1 failure") {
		t.Errorf("aggressive dropped the summary line:\n%s", aggCompact)
	}
}

func TestMix_MonotonicReduction(t *testing.T) {
	m := NewMix()
	for _, raw := range []string{mixFailRaw, mixOkRaw} {
		last := 1 << 30
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, _ := m.Format([]byte(raw), level)
			if res.BytesAfter > last {
				t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
			}
			last = res.BytesAfter
		}
	}
}

func TestMix_NonMatchingFallsBackToGeneric(t *testing.T) {
	m := NewMix()
	raw := "some unrelated log line\nanother line without build signal\n"
	res, ok := m.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
