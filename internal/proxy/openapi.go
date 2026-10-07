package proxy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"go.klarlabs.de/mcp/schema"

	"go.klarlabs.de/tokenops/internal/capability/actions"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/capability/decisions"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/capability/state"
)

// Param is one query or path parameter.
type Param struct {
	Name        string
	In          string // query or path
	Type        string // string, integer or boolean
	Description string
}

// RouteDoc describes one /api/* route for the OpenAPI document. Request
// and Response are values of the Go types the handler decodes and encodes;
// nil means a free-form JSON object (the older routes answer with maps).
type RouteDoc struct {
	Method, Path, Summary string
	Params                []Param
	Request, Response     any
}

// Pattern is the route as the mux registers it.
func (r RouteDoc) Pattern() string { return r.Method + " " + r.Path }

func q(name, typ, desc string) Param {
	return Param{Name: name, In: "query", Type: typ, Description: desc}
}

var (
	sinceParam   = q("since", "string", "RFC 3339 time or a duration such as 24h")
	untilParam   = q("until", "string", "RFC 3339 time; open when omitted")
	daysParam    = q("days", "integer", "window in days; 7 when omitted")
	allParam     = q("all", "boolean", "read all history; overrides days")
	includeParam = q("include_sources", "string", "comma-separated sources to include beyond the defaults")
)

// APICatalog is every /api/* route the daemon serves. A test holds it
// equal to the routes actually registered, and the checked-in OpenAPI
// document equal to what it generates.
var APICatalog = []RouteDoc{
	// Plans (ADR 0010 slice 1).
	{Method: "GET", Path: "/api/glance", Summary: "Session budgets, plan headroom and a ranked insight in one call", Response: headroom.GlancePayload{}},
	{Method: "GET", Path: "/api/findings", Summary: "What the coach and the session analysis observed, ranked, with what to do", Response: findings.Report{}},
	{Method: "GET", Path: "/api/plans/headroom", Summary: "Every bound plan: usage, the vendor's windows, spend against the limit, risk", Response: headroom.HeadroomPayload{}},
	{Method: "GET", Path: "/api/plans/session-budget", Summary: "Per plan window: share used, pace, and what to do", Response: headroom.BudgetPayload{}},

	// Control-plane state (slice 2).
	{Method: "GET", Path: "/api/status", Summary: "Readiness, blockers, warnings and next actions", Response: state.Status{}},
	{Method: "GET", Path: "/api/mode", Summary: "Operating mode and what each subsystem may do", Response: state.Mode{}},
	{Method: "GET", Path: "/api/coach", Summary: "The coach's dials and each power's autonomy", Response: coachcap.Report{}},
	{Method: "GET", Path: "/api/config", Summary: "The active configuration, secrets redacted"},
	{Method: "GET", Path: "/api/data-sources", Summary: "Events per source and each reader's health", Params: []Param{sinceParam, untilParam}, Response: state.DataSources{}},
	{Method: "GET", Path: "/api/vendor-usage", Summary: "Which usage sources are on and producing", Params: []Param{q("window_hours", "integer", "lookback in hours; 24 when omitted")}, Response: state.VendorUsage{}},
	{Method: "GET", Path: "/api/sources", Summary: "Each reader's ingestion health", Response: state.SourceHealth{}},
	{Method: "POST", Path: "/api/sources/refresh", Summary: "Ask the usage readers to poll now (at most every 30s)", Response: SourcesRefreshResult{}},

	// Sessions (slice 3).
	{Method: "GET", Path: "/api/dx", Summary: "Agent DX, graded, with the single change worth making", Params: []Param{daysParam, allParam}, Response: sessions.DX{}},
	{Method: "GET", Path: "/api/story", Summary: "Recent work task by task; the operator's instructions are withheld", Params: []Param{daysParam, allParam, q("limit", "integer", "most recent tasks; 10 when omitted")}, Response: sessions.Story{}},
	{Method: "GET", Path: "/api/coach/prompts", Summary: "Prompt scoring; quoted instructions are withheld", Params: []Param{sinceParam, untilParam, q("session_id", "string", "one Claude Code session"), q("limit", "integer", "most recent instructions")}, Response: sessions.Findings{}},

	// Spending (slice 3).
	{Method: "GET", Path: "/api/spend/summary", Summary: "Spend and tokens over a window", Params: []Param{sinceParam, untilParam}},
	{Method: "GET", Path: "/api/spend/series", Summary: "Spend and tokens bucketed over time", Params: []Param{sinceParam, untilParam}},
	{Method: "GET", Path: "/api/spend/forecast", Summary: "Daily spend projected forward"},
	{Method: "GET", Path: "/api/spend/cache_stats", Summary: "Prompt cache hits and savings"},
	{Method: "GET", Path: "/api/spend/commits", Summary: "What each of the operator's commits cost: the agent work that led to it, priced; subjects withheld", Params: []Param{sinceParam, q("limit", "integer", "most recent commits to list; 50 when omitted")}, Response: commits.Report{}},
	{Method: "GET", Path: "/api/spend/top", Summary: "Top consumers, ranked on the API equivalent", Params: []Param{q("by", "string", "model, provider, workflow or agent"), q("top", "integer", "how many; 5 when omitted"), sinceParam, untilParam, includeParam}, Response: spending.TopConsumers{}},
	{Method: "GET", Path: "/api/spend/burn-rate", Summary: "Usage over the last hours, hour by hour", Params: []Param{q("hours", "integer", "24 when omitted"), includeParam}, Response: spending.Burn{}},
	{Method: "GET", Path: "/api/scorecard", Summary: "The wedge KPI scorecard", Params: []Param{q("since_days", "integer", "7 when omitted")}, Response: spending.ScorecardReport{}},
	{Method: "GET", Path: "/api/pricing", Summary: "The rate card TokenOps costs with", Params: []Param{q("provider", "string", "one provider"), q("model", "string", "models containing this"), q("limit", "integer", "20 when omitted")}, Response: spending.Rates{}},

	// Workflows, optimizations, decisions.
	{Method: "GET", Path: "/api/workflows", Summary: "Recorded workflows"},
	{Method: "GET", Path: "/api/workflows/{id}", Summary: "One workflow's trace and the waste found in it", Params: []Param{{Name: "id", In: "path", Type: "string", Description: "workflow id"}}},
	{Method: "GET", Path: "/api/optimizations", Summary: "Optimization recommendations recorded"},
	{Method: "GET", Path: "/api/routing/proposals", Summary: "Model upgrades waiting on the operator", Response: decisions.Proposals{}},
	{Method: "GET", Path: "/api/decisions/{id}", Summary: "Why a decision was made", Params: []Param{{Name: "id", In: "path", Type: "string", Description: "decision id"}}, Response: decisions.Explanation{}},

	// Rules, audit, events.
	{Method: "GET", Path: "/api/rules/analyze", Summary: "Rule files analysed"},
	{Method: "GET", Path: "/api/rules/compress", Summary: "Rule files compressed"},
	{Method: "GET", Path: "/api/rules/conflicts", Summary: "Conflicts between rules"},
	{Method: "GET", Path: "/api/rules/inject", Summary: "The rules to inject for a task"},
	{Method: "GET", Path: "/api/audit", Summary: "The audit log, newest first", Params: []Param{q("action", "string", "e.g. config_change"), q("actor", "string", "e.g. api"), sinceParam, untilParam, q("limit", "integer", "")}},
	{Method: "GET", Path: "/api/domain-events", Summary: "Domain-event counts since the daemon started"},

	// Actions (slice 4): token-only, JSON bodies, audited.
	{Method: "POST", Path: "/api/mode", Summary: "Set the operating mode", Request: ModeRequest{}, Response: actions.ModeChange{}},
	{Method: "POST", Path: "/api/budgets", Summary: "Create, update or delete a budget", Request: BudgetRequest{}, Response: actions.BudgetChange{}},
	{Method: "POST", Path: "/api/routing/rules", Summary: "Create, update or delete a routing rule", Request: RoutingRuleRequest{}, Response: actions.RoutingRuleChange{}},
	{Method: "POST", Path: "/api/plans", Summary: "Bind a provider to a plan, or clear it", Request: PlanRequest{}, Response: actions.PlanChange{}},
	{Method: "POST", Path: "/api/preferred-models", Summary: "Set or clear a provider's preferred model", Request: PreferredModelRequest{}, Response: actions.PreferredModels{}},
	{Method: "POST", Path: "/api/routing/decisions", Summary: "Answer a routing proposal", Request: RoutingDecisionRequest{}, Response: actions.RoutingDecision{}},
	{Method: "POST", Path: "/api/outcomes", Summary: "Record the operator's judgement of an execution", Request: OutcomeRequest{}, Response: actions.Outcome{}},
	{Method: "POST", Path: "/api/coach", Summary: "Apply a coach preset, or change its dials", Request: coachcap.ChangeRequest{}, Response: coachcap.Report{}},
}

// OpenAPI renders the catalog as an OpenAPI 3.1 document. Schemas are
// generated from the Go types, so the document cannot describe a field the
// daemon does not send.
func OpenAPI(version string) ([]byte, error) {
	components := map[string]any{}
	paths := map[string]map[string]any{}
	for _, r := range APICatalog {
		resp, err := schemaOf(r.Response, components)
		if err != nil {
			return nil, err
		}
		op := map[string]any{
			"summary":     r.Summary,
			"operationId": operationID(r),
			"responses": map[string]any{
				"200": map[string]any{"description": "OK", "content": jsonContent(resp)},
				"400": map[string]any{"description": "The request was refused; the body says why"},
				"401": map[string]any{"description": "Missing or wrong API token"},
			},
		}
		if len(r.Params) > 0 {
			params := make([]map[string]any, 0, len(r.Params))
			for _, p := range r.Params {
				params = append(params, map[string]any{
					"name": p.Name, "in": p.In, "required": p.In == "path",
					"description": p.Description, "schema": map[string]any{"type": p.Type},
				})
			}
			op["parameters"] = params
		}
		if r.Request != nil {
			req, err := schemaOf(r.Request, components)
			if err != nil {
				return nil, err
			}
			op["requestBody"] = map[string]any{"required": true, "content": jsonContent(req)}
		}
		if paths[r.Path] == nil {
			paths[r.Path] = map[string]any{}
		}
		paths[r.Path][strings.ToLower(r.Method)] = op
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "TokenOps daemon API",
			"version": version,
			"license": map[string]any{"name": "Apache-2.0", "identifier": "Apache-2.0"},
			"description": "The local daemon's API (ADR 0010): what every surface that is not an agent reads " +
				"and changes. It serves derived figures only, never prompt text, file contents or transcripts. " +
				"Every route needs the bearer token from ~/.tokenops/daemon.url.",
		},
		"servers":  []map[string]any{{"url": "http://127.0.0.1:7878"}},
		"security": []map[string]any{{"bearer": []string{}}},
		"paths":    paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{"bearer": map[string]any{"type": "http", "scheme": "bearer"}},
			"schemas":         components,
		},
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// schemaOf generates v's schema and moves its definitions into the shared
// components, so references resolve against the OpenAPI document.
func schemaOf(v any, components map[string]any) (any, error) {
	if v == nil {
		return map[string]any{"type": "object"}, nil
	}
	s, err := schema.Generate(v)
	if err != nil {
		return nil, fmt.Errorf("schema for %T: %w", v, err)
	}
	s.Dialect = ""
	for name, def := range s.Defs {
		components[name] = def
	}
	s.Defs = nil
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var out any
	err = json.Unmarshal([]byte(strings.ReplaceAll(string(raw), `"#/$defs/`, `"#/components/schemas/`)), &out)
	return out, err
}

func jsonContent(schema any) map[string]any {
	return map[string]any{"application/json": map[string]any{"schema": schema}}
}

// operationID is a stable name for a route: getPlansHeadroom.
func operationID(r RouteDoc) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(r.Method))
	for _, part := range strings.FieldsFunc(strings.TrimPrefix(r.Path, "/api/"), func(c rune) bool {
		return c == '/' || c == '-' || c == '_' || c == '{' || c == '}'
	}) {
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}

// sortedPatterns is the catalog as mux patterns.
func sortedPatterns() []string {
	out := make([]string, 0, len(APICatalog))
	for _, r := range APICatalog {
		out = append(out, r.Pattern())
	}
	sort.Strings(out)
	return out
}
