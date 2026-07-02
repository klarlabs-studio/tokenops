package formatter

import (
	"strings"
	"testing"
)

const dotnetBuildFailRaw = `Determining projects to restore...
Restored /home/app/MyProj.csproj (in 1.2s).
MyProj -> /home/app/bin/Debug/net8.0/MyProj.dll
/home/app/Program.cs(10,5): warning CS0168: The variable 'x' is declared but never used
/home/app/Program.cs(12,3): error CS1002: ; expected
Build FAILED.
    1 Warning(s)
    1 Error(s)
`

const dotnetBuildOKRaw = `Determining projects to restore...
Restored /home/app/MyProj.csproj (in 0.8s).
MyProj -> /home/app/bin/Debug/net8.0/MyProj.dll
Build succeeded.
    0 Warning(s)
    0 Error(s)
`

func TestDotnet_CriticalSurvivesEveryLevel(t *testing.T) {
	d := NewDotnet()
	critical := []string{
		"/home/app/Program.cs(12,3): error CS1002: ; expected",
		"Build FAILED.",
		"1 Error(s)",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := d.Format([]byte(dotnetBuildFailRaw), level)
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

func TestDotnet_BalancedDropsRestoreNoise(t *testing.T) {
	d := NewDotnet()
	res, _ := d.Format([]byte(dotnetBuildFailRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"Determining projects to restore", "Restored ", " -> "} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept restore noise %q:\n%s", noise, compact)
		}
	}
	// The warning is still advisory-visible at Balanced.
	if !strings.Contains(compact, "warning CS0168") {
		t.Errorf("balanced dropped the warning line:\n%s", compact)
	}
}

func TestDotnet_AggressiveDropsWarnings(t *testing.T) {
	d := NewDotnet()
	res, _ := d.Format([]byte(dotnetBuildFailRaw), LossAggressive)
	compact := string(res.Compact)
	if strings.Contains(compact, "): warning ") {
		t.Errorf("aggressive kept a warning diagnostic:\n%s", compact)
	}
	// Error and result signal must remain.
	for _, keep := range []string{"): error ", "Build FAILED.", "1 Error(s)"} {
		if !strings.Contains(compact, keep) {
			t.Errorf("aggressive dropped critical %q:\n%s", keep, compact)
		}
	}
}

func TestDotnet_MonotonicReduction(t *testing.T) {
	d := NewDotnet()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := d.Format([]byte(dotnetBuildFailRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestDotnet_PassingBuildKeepsResult(t *testing.T) {
	d := NewDotnet()
	res, _ := d.Format([]byte(dotnetBuildOKRaw), LossBalanced)
	compact := string(res.Compact)
	if !strings.Contains(compact, "Build succeeded.") {
		t.Errorf("balanced dropped Build succeeded.:\n%s", compact)
	}
	if strings.Contains(compact, "Restored ") {
		t.Errorf("balanced kept restore noise:\n%s", compact)
	}
}

func TestDotnet_NonDotnetFallsBackToGeneric(t *testing.T) {
	d := NewDotnet()
	raw := "npm WARN deprecated foo@1.0.0\nnpm WARN deprecated bar@2.0.0\n"
	res, ok := d.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
