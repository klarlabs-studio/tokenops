package cli

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/proxy"
)

// ADR 0010: the daemon API is the read model for every surface that is not
// an agent. Every MCP tool is accounted for here, in exactly one of three
// lists, and the test checks each claim against the daemon's routes.

// apiRoute maps a tool to the route that answers the same question: a
// path for a GET, or a method and a path.
var apiRoute = map[string]string{
	"tokenops_resource_glance":     "/api/glance",
	"tokenops_plan_headroom":       "/api/plans/headroom",
	"tokenops_session_budget":      "/api/plans/session-budget",
	"tokenops_spend_summary":       "/api/spend/summary",
	"tokenops_forecast":            "/api/spend/forecast",
	"tokenops_domain_events":       "/api/domain-events",
	"tokenops_audit":               "/api/audit",
	"tokenops_optimizations":       "/api/optimizations",
	"tokenops_rules_analyze":       "/api/rules/analyze",
	"tokenops_rules_compress":      "/api/rules/compress",
	"tokenops_rules_conflicts":     "/api/rules/conflicts",
	"tokenops_rules_inject":        "/api/rules/inject",
	"tokenops_workflow_trace":      "/api/workflows/example",
	"tokenops_status":              "/api/status",
	"tokenops_mode":                "/api/mode",
	"tokenops_coach":               "/api/coach",
	"tokenops_config":              "/api/config",
	"tokenops_data_sources":        "/api/data-sources",
	"tokenops_vendor_usage_status": "/api/vendor-usage",
	"tokenops_agent_dx":            "/api/dx",
	"tokenops_story":               "/api/story",
	"tokenops_coach_prompts":       "/api/coach/prompts",
	"tokenops_top_consumers":       "/api/spend/top",
	"tokenops_burn_rate":           "/api/spend/burn-rate",
	"tokenops_scorecard":           "/api/scorecard",
	"tokenops_pricing":             "/api/pricing",
	"tokenops_routing_proposals":   "/api/routing/proposals",
	"tokenops_explain_decision":    "/api/decisions/example",
	"tokenops_plan_set":            "POST /api/plans",
	"tokenops_budget_set":          "POST /api/budgets",
	"tokenops_routing_rule_set":    "POST /api/routing/rules",
	"tokenops_preferred_model":     "POST /api/preferred-models",
	"tokenops_routing_decide":      "POST /api/routing/decisions",
	"tokenops_outcome_record":      "POST /api/outcomes",
}

// apiPending is the ADR's backlog: tools a surface needs that have no route
// yet, by slice. It is empty since slice 4: every such tool has a route. A
// new tool goes on one of the three lists.
var apiPending = map[string]int{}

// apiExempt are tools no non-agent surface needs, with the reason.
var apiExempt = map[string]string{
	"tokenops_version":        "served at /version, outside /api so probes need no credential",
	"tokenops_help":           "the agent's own orientation",
	"tokenops_explain":        "static glossary the client ships with",
	"tokenops_prepare_work":   "agent handshake around a task",
	"tokenops_review_work":    "agent handshake around a task",
	"tokenops_routing_advise": "asked by the agent before a turn",
	"tokenops_outcome_detect": "reads the calling agent's transcript",
	"tokenops_verify":         "experiment analysis run on demand by the operator's agent",
	"tokenops_experiment":     "experiment enrolment through the agent",
	"tokenops_eval":           "developer harness",
	"tokenops_replay":         "developer harness",
	"tokenops_rules_bench":    "developer harness",
	"tokenops_coverage_debt":  "developer harness over a local cover profile",
	"tokenops_fmt_analyze":    "operates on the agent's command output",
	"tokenops_fmt_learn":      "operates on the agent's command output",
	"tokenops_vendor_usage_setup": "connects a vendor login through the operator's browser and OS consent, " +
		"which belongs in the terminal or an agent conversation, not a background daemon",
}

func TestEveryToolIsAccountedForInTheDaemonAPI(t *testing.T) {
	srv := proxy.New("127.0.0.1:0",
		proxy.WithAnalytics(&proxy.AnalyticsHandlers{}),
		proxy.WithRules(&proxy.RulesHandlers{}),
		proxy.WithAudit(&proxy.AuditHandlers{}),
		proxy.WithEventCounts(func() map[string]int64 { return nil }),
		proxy.WithSourceFreshness(func() []freshness.Report { return nil }),
		proxy.WithPlans(func() headroom.Deps { return headroom.Deps{} }),
		proxy.WithState(func() state.Deps { return state.Deps{} }),
		proxy.WithSessions(func() proxy.SessionRoots { return proxy.SessionRoots{} }),
		proxy.WithActions(func() proxy.ActionDeps { return proxy.ActionDeps{} }),
		proxy.WithDashAuth(passAuth{}),
	)
	tools := mcpToolNames(t)
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		route, routed := apiRoute[name]
		_, pending := apiPending[name]
		_, exempt := apiExempt[name]
		switch n := btoi(routed) + btoi(pending) + btoi(exempt); {
		case n == 0:
			t.Errorf("%s has no API route and is not listed as pending or exempt (ADR 0010 §3)", name)
		case n > 1:
			t.Errorf("%s is listed more than once", name)
		case routed && !serves(srv, route):
			t.Errorf("%s claims %s, which the daemon does not serve", name, route)
		}
	}
	for _, list := range []map[string]bool{keys(apiRoute), keys(apiPending), keys(apiExempt)} {
		for name := range list {
			if !tools[name] {
				t.Errorf("%s is listed but no longer a tool; remove it", name)
			}
		}
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func keys[V any](m map[string]V) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// serves reports whether srv answers route: "/path" for a GET, or
// "METHOD /path".
func serves(srv *proxy.Server, route string) bool {
	method, path, ok := strings.Cut(route, " ")
	if !ok {
		method, path = http.MethodGet, route
	}
	return srv.ServesAPI(method, path)
}

// passAuth lets every request through; the write routes mount only behind
// an authenticator.
type passAuth struct{}

func (passAuth) Middleware(next http.Handler) http.Handler { return next }
