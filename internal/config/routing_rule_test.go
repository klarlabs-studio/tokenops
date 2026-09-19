package config

import (
	"strings"
	"testing"
)

// One rule's checks, callable before it is merged into a config: the MCP
// tool and the CLI verb both validate the rule they were handed, so the
// error names the argument rather than an index into a file the caller
// never saw.
func TestRoutingRuleConfigValidate(t *testing.T) {
	ok := RoutingRuleConfig{Provider: "anthropic", FromModel: "claude-opus-5*", ToModel: "claude-sonnet-5", Quality: 0.9}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid rule rejected: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*RoutingRuleConfig)
		want string
	}{
		{"no provider", func(r *RoutingRuleConfig) { r.Provider = "" }, "provider"},
		{"no from_model", func(r *RoutingRuleConfig) { r.FromModel = "" }, "from_model"},
		{"no to_model", func(r *RoutingRuleConfig) { r.ToModel = "" }, "to_model"},
		{"blank to_model", func(r *RoutingRuleConfig) { r.ToModel = "  " }, "to_model"},
		{"quality omitted", func(r *RoutingRuleConfig) { r.Quality = 0 }, "quality"},
		{"quality above one", func(r *RoutingRuleConfig) { r.Quality = 1.5 }, "quality"},
		{"quality negative", func(r *RoutingRuleConfig) { r.Quality = -0.1 }, "quality"},
		{"unknown class", func(r *RoutingRuleConfig) { r.WhenClass = "creative" }, "when_class"},
		{"window over 100", func(r *RoutingRuleConfig) { r.WhenWindowPctAbove = 101 }, "when_window_pct_above"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := ok
			tc.edit(&r)
			err := r.Validate()
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %s", err, tc.want)
			}
		})
	}
}

// Quality exactly 1 is the top of the (0,1] range, not outside it.
func TestRoutingRuleConfigAcceptsFullConfidence(t *testing.T) {
	r := RoutingRuleConfig{Provider: "openai", FromModel: "gpt-4o", ToModel: "gpt-4o-mini", Quality: 1}
	if err := r.Validate(); err != nil {
		t.Errorf("quality=1 rejected: %v", err)
	}
}
