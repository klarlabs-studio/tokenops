package formatter

import (
	"strings"
	"testing"
)

// nomadFailRaw is `nomad job status` output for a job whose allocations are
// not all healthy: two Running, one failed, one complete, one pending. It
// also carries a verbose "Recent Events:" sub-block that is Balanced+ noise.
const nomadFailRaw = `ID            = myjob
Name          = myjob
Type          = service
Status        = running
Datacenters   = dc1
Namespace     = default

Summary
Task Group  Queued  Starting  Running  Failed  Complete  Lost
web         0       0         2        1       0         0

Allocations
ID        Node ID   Task Group  Version  Desired  Status    Created
a1b2c3d4  node-01   web         3        run      running   5m ago
e5f6g7h8  node-02   web         3        run      running   5m ago
i9j0k1l2  node-03   web         3        run      failed    3m ago
m3n4o5p6  node-04   web         2        stop     complete  1h ago
q7r8s9t0  node-05   web         3        run      pending   30s ago

Recent Events:
Time                 Type      Description
2026-07-01T10:00:00  Received  Task received by client
2026-07-01T10:01:00  Setup     Building task directory
`

// nomadOkRaw is `nomad job status` output for a fully healthy job: every
// allocation is running or complete.
const nomadOkRaw = `ID            = myjob
Name          = myjob
Status        = running

Allocations
ID        Node ID   Task Group  Version  Desired  Status    Created
a1b2c3d4  node-01   web         3        run      running   5m ago
e5f6g7h8  node-02   web         3        run      running   5m ago
i9j0k1l2  node-03   web         3        run      running   5m ago
m3n4o5p6  node-04   web         3        run      complete  1h ago
`

func TestNomad_CriticalSurvivesEveryLevel(t *testing.T) {
	n := NewNomad()
	critical := []string{
		"Status",   // the "Status = running" summary line
		"Node ID",  // allocation table header (column meaning)
		"i9j0k1l2", // the failed allocation row
		"failed",
		"q7r8s9t0", // the pending allocation row
		"pending",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := n.Format([]byte(nomadFailRaw), level)
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

func TestNomad_BalancedDropsNoise(t *testing.T) {
	n := NewNomad()
	res, _ := n.Format([]byte(nomadFailRaw), LossBalanced)
	compact := string(res.Compact)

	// The verbose Recent Events sub-block is noise at Balanced+.
	for _, noise := range []string{
		"Recent Events",
		"Task received by client",
		"Building task directory",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// Summary, the allocation table, and the non-healthy rows must survive.
	for _, keep := range []string{
		"Allocations",
		"failed",
		"pending",
	} {
		if !strings.Contains(compact, keep) {
			t.Errorf("balanced dropped %q:\n%s", keep, compact)
		}
	}
	if !res.CriticalKept {
		t.Error("balanced CriticalKept=false")
	}
}

func TestNomad_AggressiveCollapsesHealthy(t *testing.T) {
	n := NewNomad()
	res, _ := n.Format([]byte(nomadFailRaw), LossAggressive)
	compact := string(res.Compact)

	// Consecutive healthy running rows collapse into a count.
	if strings.Contains(compact, "a1b2c3d4") {
		t.Errorf("aggressive should collapse healthy rows:\n%s", compact)
	}
	if !strings.Contains(compact, "healthy allocations") {
		t.Errorf("aggressive should emit a healthy-allocation count:\n%s", compact)
	}
	// The non-healthy rows must still be present.
	for _, bad := range []string{"failed", "pending"} {
		if !strings.Contains(compact, bad) {
			t.Errorf("aggressive dropped the %q row:\n%s", bad, compact)
		}
	}
}

func TestNomad_MonotonicReduction(t *testing.T) {
	n := NewNomad()
	for _, raw := range []string{nomadFailRaw, nomadOkRaw} {
		last := 1 << 30
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, _ := n.Format([]byte(raw), level)
			if res.BytesAfter > last {
				t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
			}
			last = res.BytesAfter
		}
	}
}

func TestNomad_NonNomadFallsBackToGeneric(t *testing.T) {
	n := NewNomad()
	raw := "some unrelated program output\nnothing to see here\n"
	res, ok := n.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
