package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Re-running init on a machine that is already supervised should read as a
// no-op. Telling an operator to run `tokenops daemon install` when the unit
// is already installed and running is advice that undoes nothing and teaches
// them the summary is not worth reading.
func TestDaemonUnitStepReportsAnAlreadyInstalledUnit(t *testing.T) {
	dir := t.TempDir()
	unit := filepath.Join(dir, "de.klarlabs.tokenops.plist")
	if err := os.WriteFile(unit, []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	step := daemonUnitStep(unit)
	if step.Manual {
		t.Errorf("an installed unit must not be a step the operator still owes: %+v", step)
	}
	if step.Changed {
		t.Errorf("init does not install the unit, so nothing changed: %+v", step)
	}
	if !strings.Contains(step.Detail, unit) {
		t.Errorf("detail should name the unit path, got %q", step.Detail)
	}
}

func TestDaemonUnitStepAsksForAMissingUnit(t *testing.T) {
	step := daemonUnitStep(filepath.Join(t.TempDir(), "absent.plist"))
	if !step.Manual {
		t.Fatalf("a missing unit is work the operator still owes: %+v", step)
	}
	if !strings.Contains(step.Detail, "tokenops daemon install") {
		t.Errorf("detail should name the command to run, got %q", step.Detail)
	}
}

// An unresolvable unit path is an unknown, not an absence. Claiming the unit
// is installed would be worse than asking twice.
func TestDaemonUnitStepTreatsAnUnknownPathAsOutstanding(t *testing.T) {
	step := daemonUnitStep("")
	if !step.Manual {
		t.Fatalf("an unknown unit path must not read as installed: %+v", step)
	}
}

// The trailing "Next:" line was printed unconditionally and sat outside the
// "N step(s) still need you" tally, so it neither counted nor went away.
func TestRenderSetupDoesNotAlwaysDemandDaemonInstall(t *testing.T) {
	var buf bytes.Buffer
	renderSetup(&buf, []setupStep{
		{Name: "daemon unit", Detail: "already installed (/somewhere/x.plist)"},
	})
	out := buf.String()
	if strings.Contains(out, "Next: `tokenops daemon install`") {
		t.Fatalf("summary still demands an install that is already done:\n%s", out)
	}
	if strings.Contains(out, "still need you") {
		t.Fatalf("nothing is outstanding, so nothing should be tallied:\n%s", out)
	}
}

func TestRenderSetupTalliesAnOutstandingDaemonUnit(t *testing.T) {
	var buf bytes.Buffer
	renderSetup(&buf, []setupStep{
		{Name: "daemon unit", Detail: "run `tokenops daemon install`", Manual: true},
	})
	out := buf.String()
	if !strings.Contains(out, "1 step(s) still need you") {
		t.Fatalf("an outstanding unit should be tallied with the rest:\n%s", out)
	}
}
