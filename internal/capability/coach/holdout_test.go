package coach_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
)

func writeConfig(t *testing.T, cfg config.Config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.WriteMutable(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}

// The holdout is on by default and the report says at what share.
func TestReportShowsTheHoldout(t *testing.T) {
	if got := coach.Build(config.Default()).HoldoutPercent; got != config.DefaultHoldoutPercent {
		t.Fatalf("default holdout = %d", got)
	}
}

// Opting out is a change like any other, and a preset keeps it: the
// holdout is the operator's consent, not part of an autonomy preset.
func TestHoldoutOptOutSurvivesAPreset(t *testing.T) {
	path := writeConfig(t, config.Default())
	off := 0
	r, err := coach.Change(context.Background(), path, nil, nil, time.Now(), coach.ChangeRequest{Holdout: &off})
	if err != nil || r.HoldoutPercent != 0 {
		t.Fatalf("opt out = %d, %v", r.HoldoutPercent, err)
	}
	p, _ := coach.PresetByName("guided")
	r, err = coach.ApplyPreset(path, nil, nil, p, time.Now())
	if err != nil || r.HoldoutPercent != 0 {
		t.Fatalf("after preset holdout = %d, %v; want still off", r.HoldoutPercent, err)
	}
	if r.Preset = coach.CurrentPreset(mustRead(t, path)); r.Preset != "guided" {
		t.Fatalf("holdout changed which preset matches: %q", r.Preset)
	}
	tooMuch := 25
	if _, err := coach.Change(context.Background(), path, nil, nil, time.Now(), coach.ChangeRequest{Holdout: &tooMuch}); err == nil {
		t.Fatal("a 25% holdout was accepted")
	}
}

func TestParseHoldout(t *testing.T) {
	for in, want := range map[string]int{"off": 0, "on": 10, "5": 5, "10%": 10, " 0 ": 0} {
		if got, err := coach.ParseHoldout(in); err != nil || got != want {
			t.Errorf("ParseHoldout(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "11", "-1", "lots"} {
		if _, err := coach.ParseHoldout(bad); err == nil {
			t.Errorf("ParseHoldout(%q) accepted", bad)
		}
	}
}

func mustRead(t *testing.T, path string) config.Config {
	t.Helper()
	c, err := config.ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
