package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/verify"
	"go.klarlabs.de/tokenops/internal/contexts/intervention"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
)

// The command exists to show a difference and refuse to call it a
// finding. If it only printed the number it would be the "tokens
// removed × nominal price" claim with extra steps.
func TestVerifyReportsTheDifferenceAndWhyItIsNotAFinding(t *testing.T) {
	report := verify.Report{
		BaselineCount: 12, InterventionCount: 9,
		BaselineOutcomes:     verify.OutcomeSummary{SuccessRate: measurement.Measured(90, "outcome_events")},
		InterventionOutcomes: verify.OutcomeSummary{SuccessRate: measurement.Measured(60, "outcome_events")},
		Comparison:           intervention.Comparison{Assignment: intervention.Observational},
		Verdict: intervention.Verdict{
			Observed: measurement.Measured(2000, "sqlite_events"),
			Samples:  9,
			Caveat:   "the cohorts were not assigned — executions were split by whether the intervention happened to fire",
			At:       time.Now(),
		},
	}

	var buf bytes.Buffer
	writeVerifyText(&buf, report)
	out := buf.String()

	if !strings.Contains(out, "2000") && !strings.Contains(out, "2,000") {
		t.Errorf("the measured difference is missing:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "not assigned") {
		t.Errorf("nothing explains why this is not a finding:\n%s", out)
	}
	if !strings.Contains(out, "assessed success: 90% baseline → 60% intervention") {
		t.Errorf("outcome quality comparison is missing:\n%s", out)
	}
	// The word that must not appear over an observational split.
	if strings.Contains(strings.ToLower(out), "proven") {
		t.Errorf("an observational comparison was described as proven:\n%s", out)
	}
}

// Cohort sizes are named, because "not enough data" without saying
// which side was short is advice nobody can act on.
func TestVerifyNamesBothCohortSizes(t *testing.T) {
	var buf bytes.Buffer
	writeVerifyText(&buf, verify.Report{BaselineCount: 12, InterventionCount: 0})
	out := buf.String()
	if !strings.Contains(out, "12") || !strings.Contains(out, "0") {
		t.Errorf("cohort sizes are missing:\n%s", out)
	}
}

// Harm is stated plainly. It is the one reading an operator should act
// on immediately, even though causation is unproven either way.
func TestVerifyStatesHarmPlainly(t *testing.T) {
	var buf bytes.Buffer
	writeVerifyText(&buf, verify.Report{
		BaselineCount: 30, InterventionCount: 30,
		Verdict: intervention.Verdict{
			Outcome: intervention.Harmed,
			Caveat:  "success rate fell from 95% to 50%",
			Samples: 30,
		},
	})
	if !strings.Contains(strings.ToLower(buf.String()), "harm") {
		t.Errorf("a harm verdict was not stated plainly:\n%s", buf.String())
	}
}

// A fresh install has no event store, and `verify` is a plausible first
// command to try. "sqlite: ping: unable to open database file (14)" is
// a library's error code, not an answer — it names neither what is
// missing nor what to do about it.
func TestVerifyExplainsAMissingStore(t *testing.T) {
	dir := t.TempDir()
	_, err := readVerifyEvents(newVerifyCmd(), verifyOptions{
		dbPath: filepath.Join(dir, "nothing", "events.db"),
	})
	if err == nil {
		t.Fatal("a missing store produced no error")
	}
	if !strings.Contains(err.Error(), "tokenops init") {
		t.Errorf("the error does not say how to fix it: %v", err)
	}
	if strings.Contains(err.Error(), "(14)") {
		t.Errorf("a sqlite error code reached the operator: %v", err)
	}
}
