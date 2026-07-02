package formatter

import (
	"strings"
	"testing"
)

const bazelFailRaw = `Loading: 0 packages loaded
Analyzing: 9 targets (0 packages loaded, 0 targets configured)
[0 / 3] [Prepa] BazelWorkspaceStatusAction stable-status.txt
[6 / 34] Compiling pkg/foo.cc; 2s remote-cache
[12 / 34] building //pkg:bad_test; 4s remote
INFO: Analyzed 9 targets (12 packages loaded, 340 targets configured).
INFO: Found 9 test targets...
INFO: From Compiling pkg/foo.cc:
//pkg:a_test  PASSED in 0.3s
//pkg:b_test  PASSED in 0.4s
//pkg:c_test  PASSED in 0.2s
//pkg:d_test  PASSED in 0.5s
//pkg:e_test  PASSED in 0.1s
//pkg:f_test  PASSED in 0.6s
//pkg:g_test  PASSED in 0.3s
//pkg:h_test  PASSED in 0.2s
//pkg:bad_test  FAILED in 2.1s
  /home/user/.cache/bazel/execroot/pkg/bad_test/test.log
ERROR: //pkg:bad_test: Test execution failed
src/bad.cc:42: error: undefined reference to 'foo'
Executed 9 out of 9 tests: 8 passing, 1 failing.
FAILED: Build did NOT complete successfully
`

const bazelOkRaw = `Loading: 0 packages loaded
Analyzing: 3 targets (0 packages loaded, 0 targets configured)
[2 / 12] building //pkg:app; 1s remote
INFO: Analyzed 3 targets (5 packages loaded, 120 targets configured).
INFO: Found 2 test targets...
Target //pkg:app up-to-date:
  bazel-bin/pkg/app
  bazel-bin/pkg/app.jar
//pkg:x_test  PASSED in 0.3s
//pkg:y_test  PASSED in 0.2s
INFO: Build completed successfully, 4 total actions
`

func TestBazel_CriticalSurvivesEveryLevel(t *testing.T) {
	b := NewBazel()
	critical := []string{
		"ERROR: //pkg:bad_test: Test execution failed",
		"//pkg:bad_test  FAILED in 2.1s",
		"src/bad.cc:42: error: undefined reference to 'foo'",
		"Executed 9 out of 9 tests: 8 passing, 1 failing.",
		"FAILED: Build did NOT complete successfully",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := b.Format([]byte(bazelFailRaw), level)
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

func TestBazel_BalancedDropsProgress(t *testing.T) {
	b := NewBazel()
	res, _ := b.Format([]byte(bazelFailRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"Loading:",
		"Analyzing:",
		"[12 / 34]",
		"[6 / 34]",
		"INFO: Found 9 test targets",
		"PASSED",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The failing test line and terminal FAILED line must still be present.
	if !strings.Contains(compact, "//pkg:bad_test  FAILED in 2.1s") {
		t.Errorf("balanced dropped the FAILED test line:\n%s", compact)
	}
	if !strings.Contains(compact, "FAILED: Build did NOT complete successfully") {
		t.Errorf("balanced dropped the terminal FAILED line:\n%s", compact)
	}

	// The up-to-date target block and its artifact paths drop, while the
	// terminal "Build completed" INFO summary survives.
	okRes, _ := b.Format([]byte(bazelOkRaw), LossBalanced)
	okCompact := string(okRes.Compact)
	for _, noise := range []string{
		"Target //pkg:app up-to-date:",
		"bazel-bin/pkg/app",
		"PASSED",
	} {
		if strings.Contains(okCompact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, okCompact)
		}
	}
	if !strings.Contains(okCompact, "INFO: Build completed successfully, 4 total actions") {
		t.Errorf("balanced dropped the build-completed summary:\n%s", okCompact)
	}
}

func TestBazel_MonotonicReduction(t *testing.T) {
	b := NewBazel()
	for _, raw := range []string{bazelFailRaw, bazelOkRaw} {
		last := 1 << 30
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, _ := b.Format([]byte(raw), level)
			if res.BytesAfter > last {
				t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
			}
			last = res.BytesAfter
		}
	}
}

func TestBazel_NonBazelFallsBackToGeneric(t *testing.T) {
	b := NewBazel()
	raw := "some unrelated log line\nanother line without build signal\n"
	res, ok := b.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
