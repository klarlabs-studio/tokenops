package formatter

import (
	"strings"
	"testing"
)

const swiftFailRaw = `Compiling Foo module.swift
[1/12] Compiling MyApp main.swift
[2/12] Compiling MyApp helper.swift
/Sources/App/main.swift:10:5: error: cannot find 'foo' in scope
/Sources/App/main.swift:12:9: warning: variable 'x' was never used
Test Suite 'All tests' started at 2026-07-03 10:00:00.000
Test Case '-[MyTests testExample]' passed (0.01 seconds)
Test Case '-[MyTests testBroken]' failed (0.02 seconds)
Executed 42 tests, with 1 failure (0 unexpected) in 0.5 seconds
`

const swiftPassRaw = `Compiling Foo module.swift
[1/12] Compiling MyApp main.swift
[2/12] Compiling MyApp helper.swift
Build complete! (3.42s)
Test Suite 'All tests' started at 2026-07-03 10:00:00.000
Test Case '-[MyTests testExample]' passed (0.01 seconds)
Test Case '-[MyTests testOther]' passed (0.03 seconds)
Executed 42 tests, with 0 failures (0 unexpected) in 0.5 seconds
`

func TestSwift_CriticalSurvivesEveryLevel(t *testing.T) {
	s := NewSwift()
	cases := []struct {
		name     string
		raw      string
		critical []string
	}{
		{
			name: "failing",
			raw:  swiftFailRaw,
			critical: []string{
				"/Sources/App/main.swift:10:5: error: cannot find 'foo' in scope",
				"Test Case '-[MyTests testBroken]' failed (0.02 seconds)",
				"Executed 42 tests, with 1 failure (0 unexpected) in 0.5 seconds",
			},
		},
		{
			name: "passing",
			raw:  swiftPassRaw,
			critical: []string{
				"Build complete! (3.42s)",
				"Executed 42 tests, with 0 failures (0 unexpected) in 0.5 seconds",
			},
		},
	}
	for _, tc := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := s.Format([]byte(tc.raw), level)
			if !ok {
				t.Fatalf("%s level=%s ok=false", tc.name, level)
			}
			if !res.CriticalKept {
				t.Fatalf("%s level=%s CriticalKept=false", tc.name, level)
			}
			compact := string(res.Compact)
			for _, c := range tc.critical {
				if !strings.Contains(compact, strings.TrimSpace(c)) {
					t.Errorf("%s level=%s dropped critical %q\ngot:\n%s", tc.name, level, c, compact)
				}
			}
		}
	}
}

func TestSwift_BalancedDropsNoise(t *testing.T) {
	s := NewSwift()
	res, _ := s.Format([]byte(swiftFailRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"[1/12] Compiling MyApp main.swift",
		"Compiling Foo module.swift",
		"Test Case '-[MyTests testExample]' passed",
		"Test Suite 'All tests' started",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// The advisory warning survives Balanced (only dropped at Aggressive).
	if !strings.Contains(compact, ": warning:") {
		t.Errorf("balanced should keep warnings:\n%s", compact)
	}
	// Aggressive additionally drops advisory warnings.
	resAgg, _ := s.Format([]byte(swiftFailRaw), LossAggressive)
	if strings.Contains(string(resAgg.Compact), ": warning:") {
		t.Errorf("aggressive should drop warnings:\n%s", string(resAgg.Compact))
	}
}

func TestSwift_MonotonicReduction(t *testing.T) {
	s := NewSwift()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := s.Format([]byte(swiftFailRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestSwift_NonMatchingFallsBackToGeneric(t *testing.T) {
	s := NewSwift()
	raw := "some unrelated tool output\nnothing to see here\n"
	res, ok := s.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
