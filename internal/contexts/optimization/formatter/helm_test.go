package formatter

import (
	"strings"
	"testing"
)

const helmInstallRaw = `NAME: myrelease
LAST DEPLOYED: Mon Jul  1 12:00:00 2026
NAMESPACE: default
STATUS: deployed
REVISION: 3
NOTES:
1. Get the application URL by running these commands:
  export POD_NAME=$(kubectl get pods --namespace default -l "app=myrelease" -o jsonpath="{.items[0].metadata.name}")
  export CONTAINER_PORT=$(kubectl get pod --namespace default $POD_NAME -o jsonpath="{.spec.containers[0].ports[0].containerPort}")
  echo "Visit http://127.0.0.1:8080 to use your application"
  kubectl --namespace default port-forward $POD_NAME 8080:$CONTAINER_PORT
2. It may take a few minutes for the LoadBalancer IP to be available.
   Watch the status by running kubectl get svc.
Thanks for installing the chart.
`

const helmErrorRaw = `Error: INSTALLATION FAILED: unable to build kubernetes objects from release manifest: error validating data
helm.go:84: [debug] error validating "": invalid object
`

func TestHelm_CriticalSurvivesEveryLevel(t *testing.T) {
	h := NewHelm()
	cases := map[string][]string{
		"install": {"STATUS: deployed"},
		"error":   {"Error: INSTALLATION FAILED: unable to build kubernetes objects from release manifest: error validating data"},
	}
	fixtures := map[string]string{"install": helmInstallRaw, "error": helmErrorRaw}
	for name, critical := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := h.Format([]byte(fixtures[name]), level)
			if !ok {
				t.Fatalf("%s level=%s ok=false", name, level)
			}
			if !res.CriticalKept {
				t.Fatalf("%s level=%s CriticalKept=false", name, level)
			}
			compact := string(res.Compact)
			for _, c := range critical {
				if !strings.Contains(compact, strings.TrimSpace(c)) {
					t.Errorf("%s level=%s dropped critical %q\ngot:\n%s", name, level, c, compact)
				}
			}
		}
	}
}

func TestHelm_BalancedDropsNotes(t *testing.T) {
	h := NewHelm()
	res, _ := h.Format([]byte(helmInstallRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"NOTES:", "Get the application URL", "port-forward", "Thanks for installing"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept NOTES content %q:\n%s", noise, compact)
		}
	}
	// Release metadata the agent still needs must remain.
	for _, keep := range []string{"NAME: myrelease", "STATUS: deployed", "REVISION: 3"} {
		if !strings.Contains(compact, keep) {
			t.Errorf("balanced dropped metadata %q:\n%s", keep, compact)
		}
	}
}

func TestHelm_AggressiveDropsTimestampAndNamespace(t *testing.T) {
	h := NewHelm()
	res, _ := h.Format([]byte(helmInstallRaw), LossAggressive)
	compact := string(res.Compact)
	for _, noise := range []string{"LAST DEPLOYED", "NAMESPACE: default"} {
		if strings.Contains(compact, noise) {
			t.Errorf("aggressive kept %q:\n%s", noise, compact)
		}
	}
	for _, keep := range []string{"NAME: myrelease", "STATUS: deployed", "REVISION: 3"} {
		if !strings.Contains(compact, keep) {
			t.Errorf("aggressive dropped identity %q:\n%s", keep, compact)
		}
	}
}

func TestHelm_MonotonicReduction(t *testing.T) {
	h := NewHelm()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := h.Format([]byte(helmInstallRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestHelm_NonHelmFallsBackToGeneric(t *testing.T) {
	h := NewHelm()
	raw := "some random tool output\nwith no release metadata at all\n"
	res, ok := h.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
