package mcp

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// A bad rule was refused only by the whole-config check at write time, so
// the error read "optimizer.routing_rules[3]: ..." — an index into a file
// the agent never saw — and a whitespace to_model passed outright. The
// rule is checked as given, before the file is touched.
func TestRoutingRuleSetRejectsABadRuleByArgument(t *testing.T) {
	srv, path := newModeServer(t)
	tool, _ := srv.GetTool("tokenops_routing_rule_set")
	for _, tc := range []struct {
		name, in, want string
	}{
		{"empty to_model", `{"provider":"anthropic","from_model":"x*","quality":0.9}`, "to_model"},
		{"blank to_model", `{"provider":"anthropic","from_model":"x*","to_model":"  ","quality":0.9}`, "to_model"},
		{"quality omitted", `{"provider":"anthropic","from_model":"x*","to_model":"y"}`, "quality"},
		{"quality above one", `{"provider":"anthropic","from_model":"x*","to_model":"y","quality":1.5}`, "quality"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Execute(t.Context(), []byte(tc.in))
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %s", err, tc.want)
			}
			if strings.Contains(err.Error(), "routing_rules[") {
				t.Errorf("error %q points into the config file instead of at the argument", err)
			}
		})
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Optimizer.RoutingRules) != 0 {
		t.Errorf("a rejected rule was written: %+v", cfg.Optimizer.RoutingRules)
	}
}

// Delete needs only the match keys; a rule's target and confidence are
// irrelevant to removing it.
func TestRoutingRuleSetDeleteSkipsRuleValidation(t *testing.T) {
	srv, path := newModeServer(t)
	execTool(t, srv, "tokenops_routing_rule_set", map[string]any{
		"provider": "anthropic", "from_model": "x*", "to_model": "y", "quality": 0.9,
	})
	execTool(t, srv, "tokenops_routing_rule_set", map[string]any{
		"provider": "anthropic", "from_model": "x*", "delete": true,
	})
	cfg, _ := config.ReadMutable(path)
	if len(cfg.Optimizer.RoutingRules) != 0 {
		t.Errorf("rule not deleted: %+v", cfg.Optimizer.RoutingRules)
	}
}
