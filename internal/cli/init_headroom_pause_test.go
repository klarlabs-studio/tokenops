package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// A fresh init writes the headroom pause, in shadow, at 90% of the
// tightest plan window, and installs the pause point that evaluates it.
func TestInitWritesTheHeadroomPauseInShadow(t *testing.T) {
	configPath, _, out := runInitInDir(t)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := []boundaries.RuleSpec{config.HeadroomPauseRule()}; !reflect.DeepEqual(cfg.Boundaries, want) {
		t.Fatalf("boundaries = %+v, want %+v", cfg.Boundaries, want)
	}
	rules := cfg.BoundaryRules()
	if len(rules) != 1 || rules[0].Enforced() {
		t.Fatalf("rules = %+v, want one in shadow", rules)
	}
	c := rules[0].When[0]
	if c.Field != boundaries.FieldPlanLeft || c.Op != boundaries.OpLE || c.Number != 10 || c.Window != boundaries.WindowAny {
		t.Fatalf("condition = %+v, want plan_left_pct <= 10 on the tightest window", c)
	}
	if !strings.Contains(out, "headroom pause: in shadow at 10% left") || !strings.Contains(out, "tokenops boundaries promote headroom-pause") {
		t.Errorf("init does not say what it set:\n%s", out)
	}
	settings, err := os.ReadFile(filepath.Join(filepath.Dir(configPath), ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), `"boundary-guard"`) {
		t.Errorf("the pause point was not installed:\n%s", settings)
	}
}

// --print-only shows the pause it would write.
func TestInitPrintOnlyShowsTheHeadroomPause(t *testing.T) {
	_, _, out := runInitInDir(t, "--print-only")
	for _, want := range []string{"name: headroom-pause", "action: continue", "plan_left_pct: <= 10", "stage: shadow"} {
		if !strings.Contains(out, want) {
			t.Errorf("print-only lacks %q:\n%s", want, out)
		}
	}
}

// Re-running init on an existing config adds no pause to it and installs
// no pause point it has no rule for.
func TestInitLeavesAnExistingConfigWithoutAPause(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	configPath := filepath.Join(dir, "config.yaml")
	body := []byte("listen: 127.0.0.1:7878\nmoney:\n    currency: USD\n")
	if err := os.WriteFile(configPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := withConfigFlag(newInitCmd())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--config", configPath, "--storage-path", filepath.Join(dir, "events.db"),
		"--rules-root", dir, "--repo-id", "test-repo", "--yes"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("init changed an existing config:\n%s", got)
	}
	settings, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(settings), `"boundary-guard"`) {
		t.Errorf("a pause point was installed with no rule to evaluate:\n%s", settings)
	}
}

func TestRunSetupWiresThePausePointOnlyForRulesThatNeedIt(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		want         bool
	}{
		{"no boundaries", "listen: 127.0.0.1:7878\n", false},
		{"the headroom pause", "listen: 127.0.0.1:7878\nboundaries:\n  - name: headroom-pause\n    action: continue\n    when: { plan_left_pct: \"<= 10\" }\n    require: ask\n", true},
		{"only a share rule", "listen: 127.0.0.1:7878\nboundaries:\n  - name: no-otel\n    action: share\n    when: { destination: \"== otel\" }\n    require: deny\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sandbox := t.TempDir()
			cfgPath := filepath.Join(sandbox, "config.yaml")
			if err := os.WriteFile(cfgPath, []byte(tc.config), 0o600); err != nil {
				t.Fatal(err)
			}
			settings := filepath.Join(sandbox, ".claude", "settings.json")
			var buf bytes.Buffer
			runSetup(&buf, cfgPath, setupTarget{Home: sandbox, SettingsPath: settings, Exe: "/sandbox/bin/tokenops"})
			b, err := os.ReadFile(settings)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(b), `"boundary-guard"`); got != tc.want {
				t.Fatalf("boundary-guard installed = %v, want %v\n%s", got, tc.want, b)
			}
		})
	}
}

// An install without the pause gets it only when asked: the command writes
// it in shadow, installs the pause point in the clients present, and a
// second run changes nothing.
func TestBoundariesHeadroomPauseAddsItOnRequest(t *testing.T) {
	home := boundaryHome(t, "listen: 127.0.0.1:7878\n", 0)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, nil, "boundaries", "headroom-pause")
	if err != nil || !strings.Contains(out, "headroom-pause is set, in shadow") {
		t.Fatalf("headroom-pause = %q, %v", out, err)
	}
	settings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil || !strings.Contains(string(settings), `"boundary-guard"`) {
		t.Fatalf("pause point not installed: %v\n%s", err, settings)
	}
	if out, _ := runCLI(t, nil, "boundaries", "status"); !strings.Contains(out, "headroom-pause  continue → ask  [shadow]") {
		t.Fatalf("status:\n%s", out)
	}
	again, err := runCLI(t, nil, "boundaries", "headroom-pause")
	if err != nil || !strings.Contains(again, "already set") {
		t.Fatalf("second run = %q, %v", again, err)
	}
}
