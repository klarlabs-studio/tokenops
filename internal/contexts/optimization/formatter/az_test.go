package formatter

import (
	"strings"
	"testing"
)

// azJSONRaw is `az vm list -o json`: a JSON array whose elements repeat
// structural lines the generic dedupe would corrupt, so it must be passed
// through untouched.
const azJSONRaw = `[
  {
    "name": "vm-1",
    "resourceGroup": "rg-prod",
    "powerState": "VM running"
  },
  {
    "name": "vm-2",
    "resourceGroup": "rg-dev",
    "powerState": "VM running"
  }
]
`

// azTableRaw is `az vm list -o table`: a header row, the "------" rule under
// it, data rows, and a trailing error line.
const azTableRaw = `Name    ResourceGroup    Location    PowerState
------  ---------------  ----------  ------------
vm-1    rg-prod          eastus      VM running
vm-2    rg-dev           westus      VM stopped
The resource 'vm-3' does not exist
`

func TestAz_JSONPassthrough(t *testing.T) {
	a := NewAz()
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := a.Format([]byte(azJSONRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if string(res.Compact) != azJSONRaw {
			t.Errorf("level=%s json not byte-identical:\n%s", level, res.Compact)
		}
		if !res.CriticalKept {
			t.Errorf("level=%s CriticalKept=false", level)
		}
		if !strings.Contains(res.Notes, "json passthrough") {
			t.Errorf("level=%s notes=%q, want json passthrough", level, res.Notes)
		}
	}
}

func TestAz_ErrorSurvivesEveryLevel(t *testing.T) {
	a := NewAz()
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := a.Format([]byte(azTableRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Errorf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		if !strings.Contains(compact, "does not exist") {
			t.Errorf("level=%s dropped the error line\ngot:\n%s", level, compact)
		}
	}
	// The "------" border rule is dropped at Balanced.
	res, _ := a.Format([]byte(azTableRaw), LossBalanced)
	compact := string(res.Compact)
	if strings.Contains(compact, "------  ---------------") {
		t.Errorf("balanced kept border rule:\n%s", compact)
	}
	// Header and data rows survive.
	for _, keep := range []string{"ResourceGroup", "vm-1"} {
		if !strings.Contains(compact, keep) {
			t.Errorf("balanced dropped %q:\n%s", keep, compact)
		}
	}
}

func TestAz_MonotonicReduction(t *testing.T) {
	a := NewAz()
	for _, raw := range []string{azJSONRaw, azTableRaw} {
		last := 1 << 30
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, _ := a.Format([]byte(raw), level)
			if res.BytesAfter > last {
				t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
			}
			last = res.BytesAfter
		}
	}
}

func TestAz_NonMatchingFallsBackToGeneric(t *testing.T) {
	a := NewAz()
	raw := "some unrelated program output\nnothing to see here\n"
	res, ok := a.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
