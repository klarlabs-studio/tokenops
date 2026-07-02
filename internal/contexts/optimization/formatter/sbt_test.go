package formatter

import (
	"strings"
	"testing"
)

const sbtFailRaw = `[info] welcome to sbt 1.9.7
[info] loading project definition from /proj/project
[info] resolving dependencies
[info] compiling 12 Scala sources to /proj/target/classes
[warn] one deprecation warning; re-run with -deprecation for details
[error] /proj/src/main/scala/Main.scala:10:5: type mismatch;
[error]  found   : String
[error]  required: Int
[error] one error found
[error] (Compile / compileIncremental) Compilation failed
[error] Total time: 3 s, completed Jul 2, 2026
`

const sbtOkRaw = `[info] welcome to sbt 1.9.7
[info] loading project definition from /proj/project
[info] compiling 12 Scala sources to /proj/target/classes
[success] Total time: 3 s, completed Jul 2, 2026
`

func TestSBT_CriticalSurvivesEveryLevel(t *testing.T) {
	s := NewSBT()
	critical := []string{
		"[error] /proj/src/main/scala/Main.scala:10:5: type mismatch;",
		"[error] one error found",
		"[error] (Compile / compileIncremental) Compilation failed",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := s.Format([]byte(sbtFailRaw), level)
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
	// The [success] line must survive on the passing fixture at every level.
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := s.Format([]byte(sbtOkRaw), level)
		if !strings.Contains(string(res.Compact), "[success] Total time") {
			t.Errorf("level=%s dropped [success] line:\n%s", level, res.Compact)
		}
	}
}

func TestSBT_BalancedDropsNoise(t *testing.T) {
	s := NewSBT()
	res, _ := s.Format([]byte(sbtFailRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"[info] welcome to sbt",
		"[info] resolving dependencies",
		"[info] compiling 12 Scala sources",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept info noise %q:\n%s", noise, compact)
		}
	}
	// Errors and the warn line survive at Balanced.
	if !strings.Contains(compact, "[error] one error found") {
		t.Errorf("balanced dropped an error line:\n%s", compact)
	}
	if !strings.Contains(compact, "[warn] one deprecation warning") {
		t.Errorf("balanced dropped the warn line:\n%s", compact)
	}

	// Aggressive additionally drops the [warn] advisory.
	resAgg, _ := s.Format([]byte(sbtFailRaw), LossAggressive)
	if strings.Contains(string(resAgg.Compact), "[warn] one deprecation warning") {
		t.Errorf("aggressive kept the warn line:\n%s", resAgg.Compact)
	}
	if !strings.Contains(string(resAgg.Compact), "[error] one error found") {
		t.Errorf("aggressive dropped an error line:\n%s", resAgg.Compact)
	}
}

func TestSBT_MonotonicReduction(t *testing.T) {
	s := NewSBT()
	for _, raw := range []string{sbtFailRaw, sbtOkRaw} {
		last := 1 << 30
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, _ := s.Format([]byte(raw), level)
			if res.BytesAfter > last {
				t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
			}
			last = res.BytesAfter
		}
	}
}

func TestSBT_NonMatchingFallsBackToGeneric(t *testing.T) {
	s := NewSBT()
	raw := "some unrelated log line\nanother line without build signal\n"
	res, ok := s.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
