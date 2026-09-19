package cli

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// The CLI verb and tokenops_routing_rule_set validate one rule with one
// helper, so the two surfaces refuse the same rules with the same words —
// naming the argument, not an index into config.yaml.
func TestRoutingRuleSetRefusesABadRuleByArgument(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"blank target", []string{"anthropic", "x*", " ", "--quality", "0.9"}, "to_model"},
		{"no quality", []string{"anthropic", "x*", "y"}, "quality"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := seedConfig(t)
			cmd := newRoutingRuleSetCmd()
			cmd.SetArgs(append(tc.args, "--no-restart", "--config-path", path))
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			err := cmd.Execute()
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "routing_rules[") {
				t.Errorf("error %q should name %s, not an index into the file", err, tc.want)
			}
			cfg, _ := config.ReadMutable(path)
			if len(cfg.Optimizer.RoutingRules) != 0 {
				t.Errorf("a rejected rule was written: %+v", cfg.Optimizer.RoutingRules)
			}
		})
	}
}
