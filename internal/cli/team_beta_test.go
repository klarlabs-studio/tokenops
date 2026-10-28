package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// A team plane free during its beta says so, and offers no checkout the
// server would refuse.
func TestWritePlanBeta(t *testing.T) {
	var out bytes.Buffer
	writePlan(&out, &teamwire.Plan{Status: teamwire.PlanBeta}, time.Now())
	if got := out.String(); !strings.Contains(got, "Free during the beta") {
		t.Fatalf("beta plan line missing: %q", got)
	}
}

func TestBillingNextStep(t *testing.T) {
	for status, want := range map[string]string{
		teamwire.PlanBeta:     "",
		teamwire.PlanActive:   "--portal",
		teamwire.PlanPastDue:  "--portal",
		teamwire.PlanTrialing: "--checkout",
		teamwire.PlanPaused:   "--checkout",
	} {
		got := billingNextStep(status)
		if want == "" && got != "" || !strings.Contains(got, want) {
			t.Errorf("%s: next step %q, want it to name %q", status, got, want)
		}
	}
}
