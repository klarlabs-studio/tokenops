package formatter

import (
	"strings"
	"testing"
)

// awsJSONRaw is `aws ec2 describe-instances --output json`: a JSON object
// whose array elements repeat structural `{` / `}` lines — exactly the shape
// the generic dedupe would corrupt, so it must be passed through untouched.
const awsJSONRaw = `{
    "Reservations": [
        {
            "Instances": [
                {
                    "InstanceId": "i-0abc123",
                    "State": {
                        "Name": "running"
                    }
                },
                {
                    "InstanceId": "i-0def456",
                    "State": {
                        "Name": "running"
                    }
                }
            ]
        }
    ]
}
`

// awsTableRaw is `aws ec2 describe-instances --output table`: a title rule,
// ASCII border separators, header + data rows, and a trailing error line.
const awsTableRaw = `-------------------------------------
|         DescribeInstances         |
+-----------------+-----------------+
|   InstanceId    |      State      |
+-----------------+-----------------+
|  i-0abc123      |  running        |
|  i-0def456      |  stopped        |
+-----------------+-----------------+
An error occurred (AccessDenied) when calling the DescribeInstances operation
`

func TestAws_JSONPassthrough(t *testing.T) {
	a := NewAws()
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := a.Format([]byte(awsJSONRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if string(res.Compact) != awsJSONRaw {
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

func TestAws_ErrorSurvivesEveryLevel(t *testing.T) {
	a := NewAws()
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := a.Format([]byte(awsTableRaw), level)
		if !ok {
			t.Fatalf("level=%s ok=false", level)
		}
		if !res.CriticalKept {
			t.Errorf("level=%s CriticalKept=false", level)
		}
		compact := string(res.Compact)
		for _, c := range []string{"error occurred", "AccessDenied"} {
			if !strings.Contains(compact, c) {
				t.Errorf("level=%s dropped critical %q\ngot:\n%s", level, c, compact)
			}
		}
	}
	// Border decoration is dropped at Balanced.
	res, _ := a.Format([]byte(awsTableRaw), LossBalanced)
	compact := string(res.Compact)
	for _, border := range []string{
		"+-----------------+-----------------+",
		"-------------------------------------",
	} {
		if strings.Contains(compact, border) {
			t.Errorf("balanced kept border %q:\n%s", border, compact)
		}
	}
	// Header and data rows survive.
	for _, keep := range []string{"InstanceId", "i-0abc123"} {
		if !strings.Contains(compact, keep) {
			t.Errorf("balanced dropped %q:\n%s", keep, compact)
		}
	}
}

func TestAws_MonotonicReduction(t *testing.T) {
	a := NewAws()
	for _, raw := range []string{awsJSONRaw, awsTableRaw} {
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

func TestAws_NonMatchingFallsBackToGeneric(t *testing.T) {
	a := NewAws()
	raw := "some unrelated program output\nnothing to see here\n"
	res, ok := a.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
