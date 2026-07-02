package formatter

import (
	"strings"
	"testing"
)

// ghRunListRaw is `gh run list` output: a "Showing …" preamble, a blank
// separator, and ten tab-separated rows — eight successful completions and
// two failures (each flagged with the "X" cross glyph and the word
// "failure").
const ghRunListRaw = "Showing 10 of 30 workflow runs\n" +
	"\n" +
	"✓ completed success\tRun unit tests\tCI\tmain\tpush\t9001\t1m2s\t5m\n" +
	"✓ completed success\tBuild image\tBuild\tmain\tpush\t9002\t2m10s\t8m\n" +
	"✓ completed success\tLint sources\tLint\tmain\tpush\t9003\t45s\t9m\n" +
	"✓ completed success\tType check\tTypes\tmain\tpush\t9004\t50s\t12m\n" +
	"✓ completed success\tVet code\tVet\tmain\tpush\t9005\t40s\t15m\n" +
	"✓ completed success\tFormat check\tFmt\tmain\tpush\t9007\t20s\t18m\n" +
	"✓ completed success\tSmoke tests\tSmoke\tmain\tpush\t9008\t1m5s\t22m\n" +
	"✓ completed success\tDocs build\tDocs\tmain\tpush\t9009\t30s\t25m\n" +
	"X completed failure\tDeploy prod\tDeploy\tmain\tpush\t9006\t30s\t20m\n" +
	"X completed failure\tE2E suite\tE2E\tmain\tpush\t9010\t2m0s\t1h\n"

func TestGH_CriticalSurvivesEveryLevel(t *testing.T) {
	g := NewGH()
	critical := []string{
		"Deploy prod", // failing run title
		"9006",
		"E2E suite", // failing run title
		"9010",
		"failure",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := g.Format([]byte(ghRunListRaw), level)
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

func TestGH_BalancedDropsPreamble(t *testing.T) {
	g := NewGH()
	res, _ := g.Format([]byte(ghRunListRaw), LossBalanced)
	compact := string(res.Compact)

	// The "Showing …" summary preamble is noise at Balanced+.
	if strings.Contains(compact, "Showing 10 of 30") {
		t.Errorf("balanced kept the Showing preamble:\n%s", compact)
	}
	// Every row — successful and failed — still survives at Balanced.
	for _, keep := range []string{
		"Run unit tests",
		"Docs build",
		"Deploy prod",
		"E2E suite",
	} {
		if !strings.Contains(compact, keep) {
			t.Errorf("balanced dropped %q:\n%s", keep, compact)
		}
	}
	if !res.CriticalKept {
		t.Error("balanced CriticalKept=false")
	}
}

func TestGH_AggressiveCollapsesSuccess(t *testing.T) {
	g := NewGH()
	res, _ := g.Format([]byte(ghRunListRaw), LossAggressive)
	compact := string(res.Compact)

	// Successful runs collapse into a count.
	if strings.Contains(compact, "Run unit tests") {
		t.Errorf("aggressive should collapse successful rows:\n%s", compact)
	}
	if !strings.Contains(compact, "successful") {
		t.Errorf("aggressive should emit a successful-run count:\n%s", compact)
	}
	// The failing runs must still be present.
	for _, bad := range []string{"Deploy prod", "E2E suite"} {
		if !strings.Contains(compact, bad) {
			t.Errorf("aggressive dropped the %q row:\n%s", bad, compact)
		}
	}
}

func TestGH_MonotonicReduction(t *testing.T) {
	g := NewGH()
	last := 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := g.Format([]byte(ghRunListRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestGH_NonGHFallsBackToGeneric(t *testing.T) {
	g := NewGH()
	raw := "some unrelated program output\nnothing to see here\n"
	res, ok := g.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
