package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

func TestBoundariesLoadFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `boundaries:
  - name: session-spend
    action: continue
    when: { session_cost_usd: ">= 20" }
    require: ask
  - name: big-subagents
    action: subagent_model
    when: { model_tier: "> sonnet" }
    require: approver:admin
  - name: plan-floor
    action: continue
    when: { plan_left_pct: "< 5", window: week }
    require: deny
    stage: enforce
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rules := cfg.BoundaryRules()
	if len(rules) != 3 || rules[2].Stage != boundaries.StageEnforce || rules[0].Stage != boundaries.StageShadow {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestBoundaryErrorsNameTheRule(t *testing.T) {
	cases := map[string]string{
		"unknown action": "boundaries:\n  - name: ship-it\n    action: deploy\n    require: ask\n",
		"bare number":    "boundaries:\n  - name: ship-it\n    action: continue\n    when: { session_cost_usd: 20 }\n    require: ask\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), `"ship-it"`) {
				t.Fatalf("err = %v; want the rule named", err)
			}
		})
	}
}
