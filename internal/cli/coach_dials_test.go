package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
)

func runCoachCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs(append([]string{"coach"}, args...))
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	return out.String(), err
}

func TestCoachDialsWriteAndReport(t *testing.T) {
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("coaching:\n  delivery: intervene\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	out, err := runCoachCmd(t)
	if err != nil || !strings.Contains(out, "waste   autonomous  autonomous  coaching.delivery") {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if out, err = runCoachCmd(t, "set", "waste", "ask"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "waste is advise, not ask") {
		t.Errorf("an undeliverable rung is not explained:\n%s", out)
	}
	if out, err = runCoachCmd(t, "set", "models", "ask"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "models  ask         ask") || !strings.Contains(out, "nobody is attending") {
		t.Errorf("models ask not delivered with its note:\n%s", out)
	}
	if _, err = runCoachCmd(t, "autonomy", "sometimes"); err == nil {
		t.Error("invalid autonomy accepted")
	}
	if _, err = runCoachCmd(t, "off"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Coach.Autonomy != config.AutonomyOff || cfg.Coach.Powers != nil {
		t.Errorf("coach off wrote %+v", cfg.Coach)
	}
	if cfg.Coaching.Delivery != "intervene" {
		t.Errorf("older key rewritten: %q", cfg.Coaching.Delivery)
	}
}

func TestCoachMigrateKeepsBehaviour(t *testing.T) {
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("coaching:\n  delivery: intervene\noptimizer:\n  smart_routing:\n    enabled: true\n    intervention: delegate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	before, _ := config.ReadMutable(path)
	if _, err := runCoachCmd(t, "migrate"); err != nil {
		t.Fatal(err)
	}
	after, _ := config.ReadMutable(path)
	for _, p := range config.Powers() {
		if b, a := before.CoachPower(p).Rung, after.CoachPower(p).Rung; b != a {
			t.Errorf("%s changed on migrate: %s -> %s", p, b, a)
		}
		if src := after.CoachPower(p).Source; src != "coach.powers."+p {
			t.Errorf("%s still resolved from %s after migrate", p, src)
		}
	}
}

func TestCoachStatusShowsTheLiveQuota(t *testing.T) {
	var out bytes.Buffer
	renderCoachStatus(&out, coachcap.Build(config.Default()), []string{"Claude weekly 45% · resets in 3d 2h · next tip at 50%"})
	if !strings.Contains(out.String(), "quota: Claude weekly 45% · resets in 3d 2h · next tip at 50%") {
		t.Errorf("quota line missing:\n%s", out.String())
	}
	out.Reset()
	renderCoachStatus(&out, coachcap.Build(config.Default()), nil)
	if strings.Contains(out.String(), "quota:") {
		t.Errorf("quota line without a reading:\n%s", out.String())
	}
}
