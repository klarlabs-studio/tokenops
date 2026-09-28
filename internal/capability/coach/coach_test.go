package coach_test

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
)

func power(t *testing.T, r coach.Report, name string) coach.Power {
	t.Helper()
	for _, p := range r.Powers {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("power %q not reported", name)
	return coach.Power{}
}

// A rung the coach cannot deliver yet is reported one rung lower, with the
// reason, rather than configured and silently not done.
func TestEffectiveRungsAreHonest(t *testing.T) {
	c := config.Default()
	c.Coach.Autonomy = config.AutonomyAutonomous
	r := coach.Build(c)

	if w := power(t, r, config.PowerWaste); w.Effective != config.AutonomyAutonomous || w.Reason != "" {
		t.Errorf("waste autonomous = %+v; read-guard refusing re-reads is shipped", w)
	}
	m := power(t, r, config.PowerModels)
	if m.Effective != config.AutonomyAutonomous || m.Note == "" {
		t.Errorf("models autonomous = %+v; moving subagents ships, and must say it covers subagents only", m)
	}
	c.Coach.Autonomy = config.AutonomyAsk
	r = coach.Build(c)
	// models asks through Claude Code's permission prompt (verified in an
	// interactive session); the other powers have nothing to approve.
	if m := power(t, r, config.PowerModels); m.Effective != config.AutonomyAsk || !strings.Contains(m.Note, "nobody is attending") {
		t.Errorf("models ask = %+v; it asks when attended and must say it advises otherwise", m)
	}
	for _, name := range []string{config.PowerInform, config.PowerWaste} {
		if p := power(t, r, name); p.Effective != config.AutonomyAdvise || p.Reason == "" {
			t.Errorf("%s ask = %+v; must fall back to advise, saying why", name, p)
		}
	}
}

func TestOffIsOff(t *testing.T) {
	c := config.Default()
	c.Coach.Autonomy = config.AutonomyOff
	r := coach.Build(c)
	if !r.Off() {
		t.Fatal("autonomy off does not report the coach off")
	}
	for _, p := range r.Powers {
		if p.Effective != config.AutonomyOff {
			t.Errorf("%s effective %q under autonomy off", p.Name, p.Effective)
		}
	}
}

func TestReportNamesTheSourceKey(t *testing.T) {
	c := config.Default()
	c.Coaching.Delivery = config.DeliveryIntervene
	r := coach.Build(c)
	if w := power(t, r, config.PowerWaste); w.Source != "coaching.delivery" || w.Effective != config.AutonomyAutonomous {
		t.Errorf("legacy waste = %+v", w)
	}
	if r.Verbosity != config.VerbosityNormal || r.VerbositySource != "default" {
		t.Errorf("verbosity = %q from %q", r.Verbosity, r.VerbositySource)
	}
}
