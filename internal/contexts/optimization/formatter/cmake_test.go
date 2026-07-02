package formatter

import (
	"strings"
	"testing"
)

const cmakeRaw = `-- The C compiler identification is GNU 11.4.0
-- The CXX compiler identification is GNU 11.4.0
-- Detecting C compiler ABI info
-- Detecting C compiler ABI info - done
-- Check for working C compiler: /usr/bin/cc - skipped
-- Found Threads: TRUE
-- Configuring done
-- Generating done
-- Build files have been written to: /build
[ 25%] Building CXX object CMakeFiles/app.dir/foo.cpp.o
/src/foo.cpp:10:5: warning: unused variable 'y' [-Wunused-variable]
[ 50%] Building CXX object CMakeFiles/app.dir/bar.cpp.o
/src/bar.cpp:14:9: error: 'x' was not declared in this scope
CMake Error at CMakeLists.txt:5 (add_executable):
gmake[1]: *** [CMakeFiles/app.dir/build.make:76: bar.cpp.o] Error 1
`

const cmakeSuccessRaw = `-- The C compiler identification is GNU 11.4.0
-- Detecting C compiler ABI info - done
-- Configuring done
-- Generating done
-- Build files have been written to: /build
[ 50%] Building CXX object CMakeFiles/app.dir/foo.cpp.o
[100%] Linking CXX executable app
[100%] Built target app
`

func TestCMake_CriticalSurvivesEveryLevel(t *testing.T) {
	c := NewCMake()
	critical := []string{
		"/src/bar.cpp:14:9: error: 'x' was not declared in this scope",
		"CMake Error at CMakeLists.txt:5 (add_executable):",
		"gmake[1]: *** [CMakeFiles/app.dir/build.make:76: bar.cpp.o] Error 1",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := c.Format([]byte(cmakeRaw), level)
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

func TestCMake_BalancedDropsProgress(t *testing.T) {
	c := NewCMake()
	res, _ := c.Format([]byte(cmakeRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"-- The C compiler", "-- Configuring done", "[ 25%] Building"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept configure/progress noise %q:\n%s", noise, compact)
		}
	}
	// Advisory warnings survive at Balanced, are dropped at Aggressive.
	if !strings.Contains(compact, ": warning:") {
		t.Errorf("balanced should keep advisory warnings:\n%s", compact)
	}
	aggressive, _ := c.Format([]byte(cmakeRaw), LossAggressive)
	if strings.Contains(string(aggressive.Compact), ": warning:") {
		t.Errorf("aggressive should drop advisory warnings:\n%s", aggressive.Compact)
	}
}

func TestCMake_MonotonicReduction(t *testing.T) {
	c := NewCMake()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := c.Format([]byte(cmakeRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestCMake_NonMatchingFallsBackToGeneric(t *testing.T) {
	c := NewCMake()
	raw := "deploying app v1.2.3\nall done\n"
	res, ok := c.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestCMake_CleanRunCollapses(t *testing.T) {
	c := NewCMake()
	res, ok := c.Format([]byte(cmakeSuccessRaw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !res.CriticalKept {
		t.Fatal("CriticalKept=false on a clean run")
	}
	compact := string(res.Compact)
	for _, noise := range []string{"-- The C compiler", "[ 50%] Building", "[100%] Linking"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced should drop configure/progress chatter %q:\n%s", noise, compact)
		}
	}
}

func TestCMake_ErrorsAreCritical(t *testing.T) {
	c := NewCMake()
	crit := []string{
		"/src/bar.cpp:14:9: error: 'x' was not declared in this scope",
		"main.cpp:3:1: fatal error: iostream: No such file or directory",
		"CMake Error at CMakeLists.txt:5 (add_executable):",
		"gmake[1]: *** [foo] Error 1",
		"*** No rule to make target 'x'.  Stop.",
	}
	for _, cl := range crit {
		if !c.CriticalLine(cl) {
			t.Errorf("expected critical: %q", cl)
		}
	}
	noncrit := []string{
		"/src/foo.cpp:10:5: warning: unused variable 'y'",
		"-- Configuring done",
		"-- Found Threads: TRUE",
		"[ 50%] Building CXX object CMakeFiles/app.dir/bar.cpp.o",
	}
	for _, cl := range noncrit {
		if c.CriticalLine(cl) {
			t.Errorf("expected non-critical: %q", cl)
		}
	}
}
