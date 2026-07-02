package formatter

import (
	"strings"
	"testing"
)

const golangciRaw = `internal/foo.go:12:5: Error return value of ` + "`doThing`" + ` is not checked (errcheck)
	if x := doThing(); x != nil {
	   ^
internal/bar.go:8:2: exported function Foo should have comment or be unexported (revive)
	func Foo() {
	^
internal/baz.go:20:14: ineffectual assignment to n (ineffassign)
	n := compute()
	     ^
3 issues:
* errcheck: 1
* revive: 1
* ineffassign: 1
`

func TestGolangci_FindingsSurviveEveryLevel(t *testing.T) {
	gc := NewGolangciLint()
	critical := []string{
		"internal/foo.go:12:5: Error return value of `doThing` is not checked (errcheck)",
		"internal/bar.go:8:2: exported function Foo should have comment or be unexported (revive)",
		"internal/baz.go:20:14: ineffectual assignment to n (ineffassign)",
		"3 issues:",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := gc.Format([]byte(golangciRaw), level)
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

func TestGolangci_BalancedDropsNoise(t *testing.T) {
	gc := NewGolangciLint()
	res, _ := gc.Format([]byte(golangciRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"if x := doThing()", "func Foo() {", "n := compute()", "^", "* errcheck:", "* revive:", "* ineffassign:"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// Findings and summary must remain.
	if !strings.Contains(compact, "(errcheck)") {
		t.Errorf("balanced dropped a finding:\n%s", compact)
	}
	if !strings.Contains(compact, "3 issues:") {
		t.Errorf("balanced dropped the summary:\n%s", compact)
	}
}

func TestGolangci_MonotonicReduction(t *testing.T) {
	gc := NewGolangciLint()
	last := 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := gc.Format([]byte(golangciRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestGolangci_NonMatchingFallsBackToGeneric(t *testing.T) {
	gc := NewGolangciLint()
	raw := "npm warn deprecated foo@1.0.0: use bar\nadded 214 packages in 3s\n"
	res, ok := gc.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestGolangci_FindingIsCritical(t *testing.T) {
	gc := NewGolangciLint()
	if !gc.CriticalLine("internal/foo.go:12:5: something wrong (errcheck)") {
		t.Error("golangci finding should be critical")
	}
	if !gc.CriticalLine("3 issues:") {
		t.Error("golangci summary should be critical")
	}
	if gc.CriticalLine("* revive: 1") {
		t.Error("per-linter count line should not be critical")
	}
	if gc.CriticalLine("\t   ^") {
		t.Error("caret line should not be critical")
	}
}
