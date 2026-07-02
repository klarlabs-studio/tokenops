package formatter

import (
	"strings"
	"testing"
)

// kubectlGetRaw is `kubectl get pods` output: a header plus eight rows, six
// Running and two in a non-healthy state (CrashLoopBackOff, ImagePullBackOff).
const kubectlGetRaw = `NAME                     READY   STATUS             RESTARTS   AGE
web-6f8c9d-abcde         1/1     Running            0          5d
web-6f8c9d-fghij         1/1     Running            0          5d
api-7d4b2f-klmno         1/1     Running            2          12d
worker-2a1c3e-pqrst      1/1     Running            0          3d
cache-9x8y7z-uvwxy       1/1     Running            0          8d
broken-1a2b3c-deadb      0/1     CrashLoopBackOff   6          10m
db-4d5e6f-ghijk          1/1     Running            0          20d
img-7g8h9i-lmnop         0/1     ImagePullBackOff   0          2m
`

// kubectlDescribeRaw is `kubectl describe pod` output: Key: value fields, a
// verbose Labels/Annotations blob, two Normal events, and one Warning event.
const kubectlDescribeRaw = `Name:             web-6f8c9d-abcde
Namespace:        default
Priority:         0
Node:             node-1/10.0.0.5
Start Time:       Mon, 01 Jul 2026 10:00:00 +0000
Labels:           app=web
                  pod-template-hash=6f8c9d
Annotations:      cni.projectcalico.org/podIP: 10.1.1.23/32
                  kubectl.kubernetes.io/last-applied-configuration: {"apiVersion":"v1"}
Status:           Running
IP:               10.1.1.23
Containers:
  web:
    Image:        nginx:1.25
    Port:         80/TCP
    State:        Running
    Ready:        True
Events:
  Type     Reason     Age               From               Message
  ----     ------     ----              ----               -------
  Normal   Scheduled  6m                default-scheduler  Successfully assigned default/web to node-1
  Normal   Pulled     6m                kubelet            Successfully pulled image "nginx:1.25"
  Warning  BackOff    2m (x5 over 5m)   kubelet            Back-off restarting failed container
`

func TestKubectl_CriticalSurvivesEveryLevel(t *testing.T) {
	k := NewKubectl()
	critical := []string{
		"STATUS", // header column meaning
		"broken-1a2b3c-deadb",
		"CrashLoopBackOff",
		"img-7g8h9i-lmnop",
		"ImagePullBackOff",
	}
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, ok := k.Format([]byte(kubectlGetRaw), level)
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

func TestKubectl_BalancedDropsNoise(t *testing.T) {
	k := NewKubectl()
	res, _ := k.Format([]byte(kubectlDescribeRaw), LossBalanced)
	compact := string(res.Compact)

	// Labels/Annotations blobs and Normal events are noise at Balanced+.
	for _, noise := range []string{
		"pod-template-hash",
		"last-applied-configuration",
		"Successfully assigned",
		"Successfully pulled image",
	} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept noise %q:\n%s", noise, compact)
		}
	}
	// Structure and the Warning event must survive.
	for _, keep := range []string{
		"Namespace:",
		"Events:",
		"Back-off restarting failed container",
	} {
		if !strings.Contains(compact, keep) {
			t.Errorf("balanced dropped %q:\n%s", keep, compact)
		}
	}
	if !res.CriticalKept {
		t.Error("balanced describe CriticalKept=false")
	}
}

func TestKubectl_AggressiveCollapsesHealthy(t *testing.T) {
	k := NewKubectl()
	res, _ := k.Format([]byte(kubectlGetRaw), LossAggressive)
	compact := string(res.Compact)

	// Healthy rows collapse into a count.
	if strings.Contains(compact, "web-6f8c9d-fghij") {
		t.Errorf("aggressive should collapse healthy rows:\n%s", compact)
	}
	if !strings.Contains(compact, "healthy") {
		t.Errorf("aggressive should emit a healthy-row count:\n%s", compact)
	}
	// The non-healthy rows must still be present.
	for _, bad := range []string{"CrashLoopBackOff", "ImagePullBackOff"} {
		if !strings.Contains(compact, bad) {
			t.Errorf("aggressive dropped the %q row:\n%s", bad, compact)
		}
	}
}

func TestKubectl_MonotonicReduction(t *testing.T) {
	k := NewKubectl()
	for _, raw := range []string{kubectlGetRaw, kubectlDescribeRaw} {
		last := 1 << 30
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, _ := k.Format([]byte(raw), level)
			if res.BytesAfter > last {
				t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
			}
			last = res.BytesAfter
		}
	}
}

func TestKubectl_NonKubectlFallsBackToGeneric(t *testing.T) {
	k := NewKubectl()
	raw := "some unrelated program output\nnothing to see here\n"
	res, ok := k.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
