package formatter

import (
	"strings"
	"testing"
)

const gradleFailRaw = `Starting a Gradle Daemon (subsequent builds will be faster)
Download https://repo.maven.apache.org/maven2/org/example/lib/1.0/lib-1.0.jar
<=====> 75%
> Task :app:compileJava
> Task :app:compileJava
> Task :app:compileJava
> Task :app:compileJava
> Task :app:compileJava
> Task :app:compileJava
> Task :app:compileJava
> Task :app:compileJava
> Task :app:processResources UP-TO-DATE
> Task :app:test
> Task :app:compileJava FAILED
src/Main.java:12: error: ';' expected

FAILURE: Build failed with an exception.

* What went wrong:
Execution failed for task ':app:compileJava'.

Deprecated Gradle features were used in this build, making it incompatible with Gradle 9.0.

BUILD FAILED in 4s
`

const gradleOkRaw = `Starting a Gradle Daemon (subsequent builds will be faster)
> Task :app:compileJava UP-TO-DATE
> Task :app:processResources UP-TO-DATE
> Task :app:classes UP-TO-DATE
> Task :app:jar UP-TO-DATE

BUILD SUCCESSFUL in 2s
`

func TestGradle_CriticalSurvivesEveryLevel(t *testing.T) {
	g := NewGradle()
	critical := []string{
		"> Task :app:compileJava FAILED",
		"FAILURE: Build failed with an exception.",
		"src/Main.java:12: error: ';' expected",
		"Execution failed for task ':app:compileJava'.",
		"BUILD FAILED in 4s",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := g.Format([]byte(gradleFailRaw), level)
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

func TestGradle_BalancedDropsOkTasks(t *testing.T) {
	g := NewGradle()
	res, _ := g.Format([]byte(gradleFailRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"UP-TO-DATE",
		"Starting a Gradle Daemon",
		"Download https://",
		"75%",
		"> Task :app:test",
		"Deprecated Gradle features",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The FAILED task must still be present.
	if !strings.Contains(compact, "> Task :app:compileJava FAILED") {
		t.Errorf("balanced dropped the FAILED task line:\n%s", compact)
	}
	// The plain executed-ok "> Task :app:compileJava" lines are dropped.
	if strings.Contains(compact, "> Task :app:compileJava\n") {
		t.Errorf("balanced kept an executed-ok task line:\n%s", compact)
	}
}

func TestGradle_MonotonicReduction(t *testing.T) {
	g := NewGradle()
	for _, raw := range []string{gradleFailRaw, gradleOkRaw} {
		last := 1 << 30
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, _ := g.Format([]byte(raw), level)
			if res.BytesAfter > last {
				t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
			}
			last = res.BytesAfter
		}
	}
}

func TestGradle_NonGradleFallsBackToGeneric(t *testing.T) {
	g := NewGradle()
	raw := "some unrelated log line\nanother line without build signal\n"
	res, ok := g.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
