package boundaries

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
)

func writeLimits(t *testing.T, env Env, used float64) {
	t.Helper()
	if err := claudelimits.Write(env.LimitsPath, claudelimits.Reading{
		ObservedAt: testNow.Add(-time.Minute),
		FiveHour:   &claudelimits.Window{UsedPct: used, ResetsAt: testNow.Add(2 * time.Hour).Unix()},
	}); err != nil {
		t.Fatal(err)
	}
}

// The default pause in shadow records the session it would have paused
// and lets every call through, whether or not someone is at the keyboard.
func TestTheHeadroomPauseInShadowNeverBlocks(t *testing.T) {
	for _, attended := range []bool{false, true} {
		env := testEnv(t, attended)
		r := rules(t, config.HeadroomPauseRule())
		writeLimits(t, env, 95)
		for i := range 3 {
			res, err := Pause(r, call("s1"), env)
			if err != nil {
				t.Fatal(err)
			}
			if res.Verdict != VerdictNone {
				t.Fatalf("attended=%v call %d: shadow pause = %+v, want nothing", attended, i, res)
			}
		}
		if got := len(stages(t, env, domain.DecisionID(config.HeadroomPauseName, "s1"))); got != 1 {
			t.Fatalf("attended=%v: %d shadow decisions recorded, want 1", attended, got)
		}
		pending, err := env.Dir.ListPending()
		if err != nil || len(pending) != 0 {
			t.Fatalf("attended=%v: a shadow rule left something waiting: %v %v", attended, pending, err)
		}
	}
}

func TestHeadroomPauseSummary(t *testing.T) {
	installed := map[string]bool{HarnessClaudeCode: true}
	t.Run("absent", func(t *testing.T) {
		got, err := HeadroomPause(config.Config{}, testEnv(t, false), installed)
		if err != nil {
			t.Fatal(err)
		}
		if got.Configured {
			t.Fatalf("summary = %+v, want not configured", got)
		}
	})
	t.Run("in shadow counts the sessions of the last week", func(t *testing.T) {
		env := testEnv(t, false)
		cfg := config.Config{Boundaries: []domain.RuleSpec{config.HeadroomPauseRule()}}
		r := rules(t, config.HeadroomPauseRule())
		writeLimits(t, env, 92)
		for _, s := range []string{"s1", "s1", "s2"} {
			if _, err := Pause(r, call(s), env); err != nil {
				t.Fatal(err)
			}
		}
		old := env
		old.Now = func() time.Time { return testNow.Add(-8 * 24 * time.Hour) }
		writeLimits(t, old, 92)
		if _, err := Pause(r, call("s-old"), old); err != nil {
			t.Fatal(err)
		}
		got, err := HeadroomPause(cfg, env, installed)
		if err != nil {
			t.Fatal(err)
		}
		want := PauseSummary{Configured: true, Rule: config.HeadroomPauseName, Stage: "shadow", Sessions: 2, PausePoint: true}
		if got != want {
			t.Fatalf("summary = %+v, want %+v", got, want)
		}
	})
	t.Run("no pause point", func(t *testing.T) {
		cfg := config.Config{Boundaries: []domain.RuleSpec{config.HeadroomPauseRule()}}
		got, err := HeadroomPause(cfg, testEnv(t, false), map[string]bool{})
		if err != nil {
			t.Fatal(err)
		}
		if !got.Configured || got.PausePoint {
			t.Fatalf("summary = %+v, want configured without a pause point", got)
		}
	})
}

// Adding the pause is explicit, writes it in shadow once, and leaves a
// config that already pauses on headroom alone.
func TestAddHeadroomPause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("listen: 127.0.0.1:7878\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := AddHeadroomPause(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Added || got.Rule != config.HeadroomPauseName || got.Stage != "shadow" {
		t.Fatalf("added = %+v", got)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Boundaries) != 1 || cfg.Boundaries[0].Stage != "shadow" {
		t.Fatalf("boundaries = %+v", cfg.Boundaries)
	}
	before, _ := os.ReadFile(path)
	again, err := AddHeadroomPause(path)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if again.Added || string(before) != string(after) {
		t.Fatalf("a second add changed the config: %+v\n%s", again, after)
	}

	own := filepath.Join(t.TempDir(), "config.yaml")
	body := "boundaries:\n  - name: plan-floor\n    action: continue\n    when: { plan_left_pct: \"< 5\", window: week }\n    require: deny\n    stage: enforce\n"
	if err := os.WriteFile(own, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := AddHeadroomPause(own)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(own); res.Added || res.Rule != "plan-floor" || string(b) != body {
		t.Fatalf("the operator's own pause was not respected: %+v\n%s", res, b)
	}
}
