package formatter

import (
	"slices"
	"strings"
	"testing"
)

// ansiblePlaybookRaw is a two-play `ansible-playbook` run: several tasks
// reporting a wall of "ok:" results across two hosts, a couple of "changed:"
// results, one "failed:" and one "fatal: … FAILED!" on web2, and a PLAY RECAP
// where web1 is healthy (failed=0 unreachable=0) and web2 records a failure.
const ansiblePlaybookRaw = `
PLAY [Configure web servers] ***************************************************

TASK [Gathering Facts] *********************************************************
ok: [web1]
ok: [web2]

TASK [Install nginx] ***********************************************************
changed: [web1]
ok: [web2]

TASK [Deploy config] ***********************************************************
ok: [web1]
changed: [web2]

TASK [Restart service] *********************************************************
ok: [web1]
failed: [web2] => {"changed": false, "msg": "non-zero return code from systemctl"}

PLAY [Verify deployment] *******************************************************

TASK [Gathering Facts] *********************************************************
ok: [web1]
ok: [web2]

TASK [Health check] ************************************************************
ok: [web1]
skipping: [web1]
fatal: [web2]: FAILED! => {"changed": false, "msg": "health endpoint returned 500"}

PLAY RECAP *********************************************************************
web1                       : ok=12   changed=3    unreachable=0    failed=0    skipped=1    rescued=0    ignored=0
web2                       : ok=8    changed=1    unreachable=0    failed=1    skipped=0    rescued=0    ignored=0
`

func TestAnsible_CriticalSurvivesEveryLevel(t *testing.T) {
	a := NewAnsible()
	critical := []string{
		"failed: [web2]",                    // task-level failure line
		"fatal: [web2]",                     // fatal failure line
		"FAILED!",                           // FAILED! marker
		"PLAY RECAP",                        // recap header
		"web2                       : ok=8", // the failing recap host line
		"failed=1",                          // recap failure count
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := a.Format([]byte(ansiblePlaybookRaw), level)
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

func TestAnsible_BalancedDropsOkLines(t *testing.T) {
	a := NewAnsible()
	res, _ := a.Format([]byte(ansiblePlaybookRaw), LossBalanced)
	compact := string(res.Compact)

	// "ok:" and "skipping:" per-host results are idempotency noise at Balanced+.
	if strings.Contains(compact, "ok: [web1]") {
		t.Errorf("balanced kept an ok: line:\n%s", compact)
	}
	if strings.Contains(compact, "skipping: [web1]") {
		t.Errorf("balanced kept a skipping: line:\n%s", compact)
	}
	// State changes and the recap survive.
	for _, keep := range []string{
		"changed: [web1]",
		"failed: [web2]",
		"PLAY RECAP",
	} {
		if !strings.Contains(compact, keep) {
			t.Errorf("balanced dropped %q:\n%s", keep, compact)
		}
	}
	if !res.CriticalKept {
		t.Error("balanced CriticalKept=false")
	}
}

func TestAnsible_MonotonicReduction(t *testing.T) {
	a := NewAnsible()
	last := 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := a.Format([]byte(ansiblePlaybookRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestAnsible_NonAnsibleFallsBackToGeneric(t *testing.T) {
	a := NewAnsible()
	raw := "some unrelated program output\nnothing to see here\n"
	res, ok := a.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}

func TestAnsible_AliasesIncludeAnsible(t *testing.T) {
	a := NewAnsible()
	if !slices.Contains(a.Aliases(), "ansible") {
		t.Errorf("expected Aliases to include \"ansible\", got %v", a.Aliases())
	}
}
