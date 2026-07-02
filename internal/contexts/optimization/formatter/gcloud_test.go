package formatter

import (
	"strings"
	"testing"
)

// gcloudJSONRaw is `gcloud compute instances list --format=json`: a JSON
// array whose elements repeat structural lines the generic dedupe would
// corrupt, so it must be passed through untouched.
const gcloudJSONRaw = `[
  {
    "name": "instance-1",
    "status": "RUNNING"
  },
  {
    "name": "instance-2",
    "status": "RUNNING"
  }
]
`

// gcloudTableRaw is `gcloud compute instances list`: a header row, an ASCII
// border rule, data rows, a progress line, and a trailing error line.
const gcloudTableRaw = `NAME        ZONE           STATUS
+----------+--------------+------------+
instance-1  us-central1-a  RUNNING
instance-2  us-central1-b  TERMINATED
Waiting for operation to finish...
ERROR: (gcloud.compute.instances.list) request failed: resource not found
`

func TestGcloud_JSONPassthrough(t *testing.T) {
	g := NewGcloud()
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := g.Format([]byte(gcloudJSONRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if string(res.Compact) != gcloudJSONRaw {
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

func TestGcloud_ErrorSurvivesEveryLevel(t *testing.T) {
	g := NewGcloud()
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := g.Format([]byte(gcloudTableRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Errorf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		if !strings.Contains(compact, "request failed") {
			t.Errorf("level=%s dropped the error line\ngot:\n%s", level, compact)
		}
	}
	// Border decoration and progress chatter are dropped at Balanced.
	res, _ := g.Format([]byte(gcloudTableRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{
		"+----------+--------------+------------+",
		"Waiting for operation",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// Header and data rows survive.
	for _, keep := range []string{"NAME", "instance-1"} {
		if !strings.Contains(compact, keep) {
			t.Errorf("balanced dropped %q:\n%s", keep, compact)
		}
	}
}

func TestGcloud_MonotonicReduction(t *testing.T) {
	g := NewGcloud()
	for _, raw := range []string{gcloudJSONRaw, gcloudTableRaw} {
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

func TestGcloud_NonMatchingFallsBackToGeneric(t *testing.T) {
	g := NewGcloud()
	raw := "some unrelated program output\nnothing to see here\n"
	res, ok := g.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
