package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	mcpgo "go.klarlabs.de/mcp"
	mcpserver "go.klarlabs.de/mcp/server"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/prompts"
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard"
	"go.klarlabs.de/tokenops/internal/contexts/rules"
)

// Agents read every tool definition before their first call. Fifty tools
// cost about 22,600 tokens of context in every session and left the agent
// to choose between near neighbours — spend_summary, top_consumers,
// burn_rate and forecast — that answer one question in four slices. So
// the server registers each tool as before and then Consolidate moves them
// behind sixteen public ones: each answers one question, picks its slice
// with a view, action or setting, and carries a title and annotations a
// client can decide approval on. The handlers, their validation and their
// tests are unchanged; the public layer only routes.

// route is one view, action or setting of a public tool.
type route struct {
	// inner is the internal tool that answers it.
	inner string
	// params are the public parameters it takes, by JSON name.
	params []string
	// rename maps a public parameter to the inner tool's name for it.
	rename map[string]string
}

// kind is what a public tool may do, for its annotations.
type kind int

const (
	// reads changes nothing and answers the same way twice.
	reads kind = iota
	// records writes evidence (advice given, an outcome) but changes no
	// setting.
	records
	// changes alters settings; a removal is part of it.
	changes
)

// publicTool is one tool the server lists.
type publicTool struct {
	name, title, description string
	// selector names the parameter that picks the route: view, action or
	// setting. Empty for a tool with one route.
	selector string
	// def is the route taken when the selector is omitted.
	def    string
	routes map[string]route
	kind   kind
	// input and output are zero values of the tool's input and result
	// types, for their schemas.
	input, output any
	// pick chooses the route from the arguments instead of a selector.
	pick func(args map[string]any) string
}

// selected is the route the arguments ask for.
func (p publicTool) selected(args map[string]any) (string, error) {
	if p.pick != nil {
		return p.pick(args), nil
	}
	if p.selector == "" {
		return p.def, nil
	}
	v, _ := args[p.selector].(string)
	delete(args, p.selector)
	if v == "" {
		v = p.def
	}
	if _, ok := p.routes[v]; !ok {
		return "", &mcpgo.ToolInputError{Message: fmt.Sprintf("%s %q is not one of: %s", p.selector, v, strings.Join(p.routeNames(), ", "))}
	}
	return v, nil
}

func (p publicTool) routeNames() []string {
	names := make([]string, 0, len(p.routes))
	for n := range p.routes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Consolidate replaces every tool registered on s with the public tools.
// The registered tools stay reachable only through them. A public route
// whose tool was not registered is left out, and a public tool with no
// route left is not listed.
func Consolidate(s *Server) error {
	inner := map[string]*mcpserver.Tool{}
	for _, info := range s.Tools() {
		if t, ok := s.GetTool(info.Name); ok {
			inner[info.Name] = t
		}
	}
	for name := range inner {
		s.RemoveTool(name)
	}
	for _, p := range publicTools() {
		available := map[string]route{}
		for name, r := range p.routes {
			if _, ok := inner[r.inner]; ok {
				available[name] = r
			}
		}
		if len(available) == 0 {
			continue
		}
		p.routes = available
		if _, ok := available[p.def]; !ok && p.selector != "" {
			p.def = p.routeNames()[0]
		}
		if err := registerPublic(s, p, inner); err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
	}
	return nil
}

// registerPublic registers p, its handler typed on p.input so the builder
// derives the input schema from it.
func registerPublic(s *Server, p publicTool, inner map[string]*mcpserver.Tool) error {
	in := reflect.TypeOf(p.input)
	fnType := reflect.FuncOf(
		[]reflect.Type{reflect.TypeFor[context.Context](), in},
		[]reflect.Type{reflect.TypeFor[any](), reflect.TypeFor[error]()}, false)
	fn := reflect.MakeFunc(fnType, func(args []reflect.Value) []reflect.Value {
		out, err := p.call(args[0].Interface().(context.Context), args[1].Interface(), inner)
		errVal := reflect.Zero(reflect.TypeFor[error]())
		if err != nil {
			errVal = reflect.ValueOf(err)
		}
		outVal := reflect.Zero(reflect.TypeFor[any]())
		if out != nil {
			outVal = reflect.ValueOf(&out).Elem()
		}
		return []reflect.Value{outVal, errVal}
	})
	b := s.Tool(p.name).Title(p.title).Description(p.description)
	switch p.kind {
	case reads:
		b = b.ReadOnly().Idempotent().ClosedWorld()
	case records:
		b = b.Annotations(mcpgo.ToolAnnotations{Title: p.title, ReadOnlyHint: ptr(false), DestructiveHint: ptr(false), IdempotentHint: ptr(false), OpenWorldHint: ptr(false)})
	case changes:
		b = b.Annotations(mcpgo.ToolAnnotations{Title: p.title, ReadOnlyHint: ptr(false), DestructiveHint: ptr(true), IdempotentHint: ptr(true), OpenWorldHint: ptr(false)})
	}
	if p.output != nil {
		b = b.OutputSchema(p.output)
	}
	b.Handler(fn.Interface())
	return s.Err()
}

func ptr[T any](v T) *T { return &v }

// call routes one invocation to its inner tool.
func (p publicTool) call(ctx context.Context, in any, inner map[string]*mcpserver.Tool) (any, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	args := map[string]any{}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	name, err := p.selected(args)
	if err != nil {
		return nil, err
	}
	r := p.routes[name]
	innerArgs := map[string]any{}
	allowed := map[string]bool{}
	for _, k := range r.params {
		allowed[k] = true
	}
	var stray []string
	for k, v := range args {
		if !allowed[k] {
			stray = append(stray, k)
			continue
		}
		if to, ok := r.rename[k]; ok {
			k = to
		}
		innerArgs[k] = v
	}
	if len(stray) > 0 {
		sort.Strings(stray)
		takes := "no parameters"
		if len(r.params) > 0 {
			takes = strings.Join(r.params, ", ")
		}
		what := p.name
		if p.selector != "" {
			what = p.selector + " " + name
		}
		return nil, &mcpgo.ToolInputError{Message: fmt.Sprintf("%s does not apply to %s, which takes %s", strings.Join(stray, ", "), what, takes)}
	}
	body, err := json.Marshal(innerArgs)
	if err != nil {
		return nil, err
	}
	res, err := inner[r.inner].Execute(ctx, body)
	if err != nil {
		return nil, err
	}
	if p.selector == "" && p.pick == nil {
		return res, nil // one route: the inner answer as it is
	}
	if sr, ok := structured(res); ok && sr.IsError {
		return sr, nil
	}
	payload, err := payloadOf(res)
	if err != nil {
		return nil, err
	}
	key := p.selector
	if key == "" {
		key = "view"
	}
	return map[string]any{key: name, name: payload}, nil
}

// structured unwraps a StructuredResult.
func structured(res any) (*mcpgo.StructuredResult, bool) {
	switch v := res.(type) {
	case mcpgo.StructuredResult:
		return &v, true
	case *mcpgo.StructuredResult:
		return v, v != nil
	}
	return nil, false
}

// payloadOf is an inner answer as JSON: its structured content, the JSON
// a tool returned as text, or the text itself.
func payloadOf(res any) (json.RawMessage, error) {
	if sr, ok := structured(res); ok {
		if sr.StructuredContent != nil {
			return json.Marshal(sr.StructuredContent)
		}
		var texts []string
		for _, c := range sr.Content {
			if b, err := json.Marshal(c); err == nil {
				texts = append(texts, string(b))
			}
		}
		return json.Marshal(texts)
	}
	if s, ok := res.(string); ok {
		if json.Valid([]byte(s)) {
			return json.RawMessage(s), nil
		}
		// A summary with its figures underneath: the figures are the answer.
		if raw, ok := jsonFromMarkdownPayload(s); ok {
			return raw, nil
		}
		return json.Marshal(map[string]string{"text": s})
	}
	return json.Marshal(res)
}

// --- the public tools ------------------------------------------------------

// Inputs. Every parameter a route takes is here, described once.

type glanceIn struct {
	View string `json:"view,omitempty" jsonschema:"enum=all,enum=headroom,enum=session_budget,enum=findings,description=all (default): budgets, headroom and one ranked insight. headroom: each plan's usage, the vendor's windows with pace, spend against the limit, risk. session_budget: the current window's share used and what to do. findings: what the coach and the session analysis observed, ranked, with what to do."`
}

type spendIn struct {
	View           string   `json:"view,omitempty" jsonschema:"enum=summary,enum=top,enum=burn,enum=forecast,description=summary (default): spend and tokens over a window. top: the largest consumers. burn: the last hours, hour by hour. forecast: daily spend projected forward."`
	Since          string   `json:"since,omitempty" jsonschema:"description=summary and top: RFC3339 time or a duration such as 24h or 7d"`
	Until          string   `json:"until,omitempty" jsonschema:"description=summary and top: RFC3339 time; open when omitted"`
	WorkflowID     string   `json:"workflow_id,omitempty" jsonschema:"description=summary: one workflow"`
	AgentID        string   `json:"agent_id,omitempty" jsonschema:"description=summary: one agent"`
	By             string   `json:"by,omitempty" jsonschema:"enum=model,enum=provider,enum=workflow,enum=agent,description=top: what to rank (default model)"`
	Top            int      `json:"top,omitempty" jsonschema:"minimum=1,maximum=50,description=top: how many (default 5)"`
	Hours          int      `json:"hours,omitempty" jsonschema:"minimum=1,maximum=168,description=burn: lookback in hours (default 24)"`
	HorizonDays    int      `json:"horizon_days,omitempty" jsonschema:"minimum=1,maximum=30,description=forecast: days ahead (default 7)"`
	IncludeSources []string `json:"include_sources,omitempty" jsonschema:"description=re-admit an excluded activity source: mcp-session"`
}

type sessionsIn struct {
	View      string `json:"view,omitempty" jsonschema:"enum=dx,enum=story,enum=prompts,description=dx (default): how sessions go, graded, with the one change worth making. story: recent work task by task. prompts: the operator's instructions scored against prompting heuristics (quotes withheld)."`
	Days      int    `json:"days,omitempty" jsonschema:"description=dx and story: window in days (default 7)"`
	All       bool   `json:"all,omitempty" jsonschema:"description=dx and story: read all history; overrides days"`
	Limit     int    `json:"limit,omitempty" jsonschema:"description=story: most recent tasks (default 10, 0 for all). prompts: most recent instructions"`
	Since     string `json:"since,omitempty" jsonschema:"description=prompts: RFC3339 time or a duration such as 24h (default 7d)"`
	Until     string `json:"until,omitempty" jsonschema:"description=prompts: RFC3339 time"`
	SessionID string `json:"session_id,omitempty" jsonschema:"description=prompts: one Claude Code session"`
}

type statusIn struct {
	View        string `json:"view,omitempty" jsonschema:"enum=status,enum=mode,enum=config,enum=data_sources,enum=vendor_usage,enum=version,description=status (default): readiness, blockers, warnings and next actions. mode: what TokenOps may do on its own. config: the active configuration, secrets redacted. data_sources: events per source and each reader's health. vendor_usage: which usage readers are on and producing. version: this server's build."`
	Since       string `json:"since,omitempty" jsonschema:"description=data_sources: RFC3339 time or a duration (default 30d)"`
	Until       string `json:"until,omitempty" jsonschema:"description=data_sources: RFC3339 time"`
	WindowHours int    `json:"window_hours,omitempty" jsonschema:"description=vendor_usage: lookback in hours (default 24)"`
}

type explainIn struct {
	Term       string `json:"term,omitempty" jsonschema:"description=A figure to explain, e.g. wall-clock, rework, api-equivalent. Empty lists every term."`
	DecisionID string `json:"decision_id,omitempty" jsonschema:"description=A decision ID from a TokenOps recommendation, to see its evidence, alternatives, policy and result. Takes precedence over term."`
}

type recordsIn struct {
	View         string  `json:"view,omitempty" jsonschema:"enum=optimizations,enum=audit,enum=events,enum=workflow,enum=scorecard,enum=verify,description=optimizations (default): optimizations recommended or applied. audit: config, plan and budget changes, optimizations, exports. events: counts of each domain event. workflow: one workflow's steps with waste findings (needs workflow_id). scorecard: time to first value, token savings, spend attribution. verify: resource use and outcomes with and without optimizations."`
	Since        string  `json:"since,omitempty" jsonschema:"description=optimizations and audit: RFC3339 time or a duration"`
	Until        string  `json:"until,omitempty" jsonschema:"description=optimizations and audit: RFC3339 time"`
	WorkflowID   string  `json:"workflow_id,omitempty" jsonschema:"description=optimizations: one workflow. workflow: the workflow to trace (required)"`
	AgentID      string  `json:"agent_id,omitempty" jsonschema:"description=optimizations: one agent"`
	Limit        int     `json:"limit,omitempty" jsonschema:"description=optimizations and audit: most recent rows (audit default 50)"`
	Action       string  `json:"action,omitempty" jsonschema:"description=audit: one action, e.g. config_change"`
	Actor        string  `json:"actor,omitempty" jsonschema:"description=audit: one actor, e.g. api"`
	SinceDays    int     `json:"since_days,omitempty" jsonschema:"description=scorecard: window in days"`
	FVTSeconds   float64 `json:"fvt_seconds,omitempty" jsonschema:"description=scorecard: a measured time to first value to grade, in seconds"`
	TEUPct       float64 `json:"teu_pct,omitempty" jsonschema:"description=scorecard: a measured token-efficiency uplift to grade, in percent"`
	SACPct       float64 `json:"sac_pct,omitempty" jsonschema:"description=scorecard: a measured spend-attribution completeness to grade, in percent"`
	BaselineRef  string  `json:"baseline_ref,omitempty" jsonschema:"description=scorecard: the baseline to compare against"`
	Days         int     `json:"days,omitempty" jsonschema:"description=verify: window in days (default 30, 0 for all)"`
	ExperimentID string  `json:"experiment_id,omitempty" jsonschema:"description=verify: the experiment to compare; required when several are present"`
}

type rulesIn struct {
	View                string   `json:"view,omitempty" jsonschema:"enum=analyze,enum=conflicts,enum=compress,enum=inject,description=analyze (default): the agent rules files, sized and scored. conflicts: rules that contradict or repeat each other. compress: a shorter rule set that keeps quality. inject: the rules that matter for one piece of work, within a token budget."`
	Root                string   `json:"root,omitempty" jsonschema:"description=Repository root (default: the working directory)"`
	RepoID              string   `json:"repo_id,omitempty" jsonschema:"description=Stable repository identifier"`
	Provider            string   `json:"provider,omitempty" jsonschema:"enum=openai,enum=anthropic,enum=gemini,description=analyze: tokenize for this provider"`
	SimilarityThreshold float64  `json:"similarity_threshold,omitempty" jsonschema:"description=compress: how alike two rules must be to merge (0-1)"`
	QualityFloor        float64  `json:"quality_floor,omitempty" jsonschema:"description=compress: the lowest quality score allowed (0-1)"`
	WorkflowID          string   `json:"workflow_id,omitempty" jsonschema:"description=inject: the workflow the rules are for"`
	AgentID             string   `json:"agent_id,omitempty" jsonschema:"description=inject: the agent the rules are for"`
	Files               []string `json:"files,omitempty" jsonschema:"description=inject: files the work touches"`
	Tools               []string `json:"tools,omitempty" jsonschema:"description=inject: tools the work uses"`
	Keywords            []string `json:"keywords,omitempty" jsonschema:"description=inject: words that describe the work"`
	TokenBudget         int64    `json:"token_budget,omitempty" jsonschema:"description=inject: most tokens of rules to return"`
	MinScore            float64  `json:"min_score,omitempty" jsonschema:"description=inject: lowest relevance to include (0-1)"`
	IncludeGlobal       bool     `json:"include_global,omitempty" jsonschema:"description=inject: include rules that apply everywhere"`
}

type fmtIn struct {
	View       string `json:"view,omitempty" jsonschema:"enum=learn,enum=analyze,description=learn (default): which command outputs to compress next, and any over-compression. analyze: how much command output in past sessions could be compressed."`
	Limit      int    `json:"limit,omitempty" jsonschema:"description=learn: most commands per list (default 20); the totals are always given"`
	Root       string `json:"root,omitempty" jsonschema:"description=analyze: Claude Code projects directory (default ~/.claude/projects)"`
	MaxFiles   int    `json:"max_files,omitempty" jsonschema:"description=analyze: most sessions to scan, newest first"`
	RecoverDir string `json:"recover_dir,omitempty" jsonschema:"description=learn: recovery store (default ~/.tokenops/recovery)"`
	NoJSONL    bool   `json:"no_jsonl,omitempty" jsonschema:"description=learn: skip the Claude Code logs"`
}

type outcomeIn struct {
	Action           string   `json:"action,omitempty" jsonschema:"enum=record,enum=detect,description=record (default): the operator's assessment of a piece of work. detect: read the local verifier result after the session's last edit."`
	ExecutionID      string   `json:"execution_id" jsonschema:"required,description=The execution being assessed"`
	DecisionID       string   `json:"decision_id,omitempty" jsonschema:"description=The decision being evaluated, when known"`
	Result           string   `json:"result,omitempty" jsonschema:"enum=achieved,enum=partial,enum=not_achieved,description=record: how the work turned out"`
	Caveat           string   `json:"caveat,omitempty" jsonschema:"description=record: why it was partial or unsuccessful, or context for the assessment"`
	AttentionMinutes *float64 `json:"attention_minutes,omitempty" jsonschema:"description=record: active human effort in minutes, only when the operator reports it"`
	SessionID        string   `json:"session_id,omitempty" jsonschema:"description=detect: the Claude Code session whose transcript to check"`
}

type routingIn struct {
	Action      string   `json:"action,omitempty" jsonschema:"enum=advise,enum=proposals,enum=decide,enum=set_rule,description=advise (default): which model a turn should run on, from measured signal; it only suggests. proposals: model upgrades waiting on the operator. decide: approve or deny a proposal. set_rule: add, update or remove a routing rule."`
	Instruction string   `json:"instruction,omitempty" jsonschema:"description=advise: the instruction about to be acted on, verbatim; classified locally, never stored"`
	Provider    string   `json:"provider,omitempty" jsonschema:"description=advise and set_rule: the provider, e.g. anthropic"`
	Model       string   `json:"model,omitempty" jsonschema:"description=advise: the model the turn would otherwise run on"`
	ToolDensity float64  `json:"tool_density,omitempty" jsonschema:"description=advise: share of the recent exchange that was tool traffic (0-1)"`
	WorkID      string   `json:"work_id,omitempty" jsonschema:"description=advise: stable work identifier"`
	ExecutionID string   `json:"execution_id,omitempty" jsonschema:"description=advise: stable execution identifier"`
	ActorID     string   `json:"actor_id,omitempty" jsonschema:"description=advise: the actor performing the execution"`
	WorkflowID  string   `json:"workflow_id,omitempty" jsonschema:"description=advise: workflow identifier to keep across prepare, execution and review"`
	Key         string   `json:"key,omitempty" jsonschema:"description=decide: the proposal's key from proposals (provider|from_model|to_model)"`
	Decision    string   `json:"decision,omitempty" jsonschema:"enum=approve,enum=deny,description=decide: approve routes matching requests to the proposed model; deny keeps the requested one"`
	FromModel   string   `json:"from_model,omitempty" jsonschema:"description=set_rule: the model to match; a trailing * matches a prefix"`
	ToModel     string   `json:"to_model,omitempty" jsonschema:"description=set_rule: the cheaper target model (unless delete)"`
	Quality     float64  `json:"quality,omitempty" jsonschema:"description=set_rule: confidence (0-1] that to_model keeps quality (unless delete)"`
	Fallbacks   []string `json:"fallbacks,omitempty" jsonschema:"description=set_rule: models to try if to_model fails"`
	Delete      bool     `json:"delete,omitempty" jsonschema:"description=set_rule: remove the rule for provider and from_model"`
}

type configureIn struct {
	Setting       string  `json:"setting" jsonschema:"required,enum=plan,enum=budget,enum=mode,enum=preferred_model,enum=usage_meter,description=plan: bind a subscription plan to a provider (omit provider to list them). budget: set or remove a spend budget. mode: passive or active. preferred_model: the most expensive model a provider may be routed to. usage_meter: connect claude.ai's own usage meter."`
	Provider      string  `json:"provider,omitempty" jsonschema:"description=plan and preferred_model: the provider, e.g. anthropic"`
	Plan          string  `json:"plan,omitempty" jsonschema:"description=plan: a plan name from the catalog, e.g. claude-max-20x"`
	SpendLimitUSD float64 `json:"spend_limit_usd,omitempty" jsonschema:"description=plan: spend limit in USD for a plan billed at API rates"`
	LimitWindow   string  `json:"limit_window,omitempty" jsonschema:"enum=monthly,enum=weekly,enum=daily,description=plan: period the spend limit covers (default monthly)"`
	RateFactor    float64 `json:"rate_factor,omitempty" jsonschema:"description=plan: scale estimated spend to a negotiated rate (0.8 = 20% off list)"`
	Price         float64 `json:"price,omitempty" jsonschema:"description=plan: what the operator pays per month, as on the bill; only when they state it"`
	Currency      string  `json:"currency,omitempty" jsonschema:"description=plan: ISO 4217 code of price, e.g. EUR"`
	Since         string  `json:"since,omitempty" jsonschema:"description=plan: the date the plan took effect, when before today; only when the operator says so"`
	Clear         bool    `json:"clear,omitempty" jsonschema:"description=plan and preferred_model: remove the binding or the ceiling"`
	Name          string  `json:"name,omitempty" jsonschema:"description=budget: its name; set replaces by name"`
	Window        string  `json:"window,omitempty" jsonschema:"enum=daily,enum=weekly,enum=monthly,description=budget: calendar window (default monthly)"`
	LimitUSD      float64 `json:"limit_usd,omitempty" jsonschema:"description=budget: ceiling in USD, for basis spend or equivalent"`
	LimitTokens   int64   `json:"limit_tokens,omitempty" jsonschema:"description=budget: ceiling in tokens, for basis tokens"`
	WarnAt        float64 `json:"warn_at,omitempty" jsonschema:"description=budget: fraction of the limit that warns (default 0.75)"`
	CritAt        float64 `json:"crit_at,omitempty" jsonschema:"description=budget: fraction of the limit that is critical (default 0.95)"`
	WorkflowID    string  `json:"workflow_id,omitempty" jsonschema:"description=budget: limit it to one workflow"`
	AgentID       string  `json:"agent_id,omitempty" jsonschema:"description=budget: limit it to one agent"`
	Basis         string  `json:"basis,omitempty" jsonschema:"enum=spend,enum=equivalent,enum=tokens,description=budget: what the limit watches: billed spend (default), API-price equivalent, or tokens. On a flat plan billed spend is about 0, so use tokens or equivalent."`
	Delete        bool    `json:"delete,omitempty" jsonschema:"description=budget: remove the budget with this name"`
	Mode          string  `json:"mode,omitempty" jsonschema:"enum=passive,enum=active,description=mode: passive analyses on demand; active adds live routing and a budget watcher"`
	Model         string  `json:"model,omitempty" jsonschema:"description=preferred_model: the model to stay on"`
	Org           string  `json:"org,omitempty" jsonschema:"description=usage_meter: the organization to meter, by name or UUID"`
}

// Results: the route taken and its answer, under the route's name.

type glanceOut struct {
	View          string                `json:"view"`
	All           *resourceGlanceResult `json:"all,omitempty"`
	Headroom      *planHeadroomResult   `json:"headroom,omitempty"`
	SessionBudget map[string]any        `json:"session_budget,omitempty"`
	Findings      *findings.Report      `json:"findings,omitempty"`
}

type spendOut struct {
	View     string              `json:"view"`
	Summary  *spendSummaryResult `json:"summary,omitempty"`
	Top      *topConsumersResult `json:"top,omitempty"`
	Burn     map[string]any      `json:"burn,omitempty"`
	Forecast *forecastResult     `json:"forecast,omitempty"`
}

type sessionsOut struct {
	View    string            `json:"view"`
	DX      *agentDXResult    `json:"dx,omitempty"`
	Story   *storyResult      `json:"story,omitempty"`
	Prompts *prompts.Findings `json:"prompts,omitempty"`
}

type statusOut struct {
	View        string             `json:"view"`
	Status      *statusResult      `json:"status,omitempty"`
	Mode        map[string]any     `json:"mode,omitempty"`
	Config      map[string]any     `json:"config,omitempty"`
	DataSources *dataSourcesResult `json:"data_sources,omitempty"`
	VendorUsage *vendorUsageResult `json:"vendor_usage,omitempty"`
	Version     *versionResult     `json:"version,omitempty"`
}

type explainOut struct {
	View     string                 `json:"view"`
	Term     *explainResult         `json:"term,omitempty"`
	Decision *explainDecisionResult `json:"decision,omitempty"`
}

type recordsOut struct {
	View          string               `json:"view"`
	Optimizations *optimizationsResult `json:"optimizations,omitempty"`
	Audit         *auditResult         `json:"audit,omitempty"`
	Events        *domainEventsResult  `json:"events,omitempty"`
	Workflow      *workflowTraceResult `json:"workflow,omitempty"`
	Scorecard     *scorecard.Scorecard `json:"scorecard,omitempty"`
	Verify        *verifyResult        `json:"verify,omitempty"`
}

type rulesOut struct {
	View      string                 `json:"view"`
	Analyze   *rulesAnalyzeResult    `json:"analyze,omitempty"`
	Conflicts *rulesConflictsResult  `json:"conflicts,omitempty"`
	Compress  *rulesCompressResult   `json:"compress,omitempty"`
	Inject    *rules.SelectionResult `json:"inject,omitempty"`
}

type fmtOut struct {
	View    string         `json:"view"`
	Learn   map[string]any `json:"learn,omitempty"`
	Analyze map[string]any `json:"analyze,omitempty"`
}

type outcomeOut struct {
	Action string         `json:"action"`
	Record *outcomeResult `json:"record,omitempty"`
	Detect *outcomeResult `json:"detect,omitempty"`
}

type routingOut struct {
	Action    string               `json:"action"`
	Advise    *routingAdviceResult `json:"advise,omitempty"`
	Proposals any                  `json:"proposals,omitempty"`
	Decide    map[string]any       `json:"decide,omitempty"`
	SetRule   map[string]any       `json:"set_rule,omitempty"`
}

type configureOut struct {
	Setting        string         `json:"setting"`
	Plan           map[string]any `json:"plan,omitempty"`
	Budget         map[string]any `json:"budget,omitempty"`
	Mode           map[string]any `json:"mode,omitempty"`
	PreferredModel map[string]any `json:"preferred_model,omitempty"`
	UsageMeter     map[string]any `json:"usage_meter,omitempty"`
}

func params(names ...string) []string { return names }

// publicTools are the sixteen tools the server lists.
func publicTools() []publicTool {
	window := params("since", "until")
	return []publicTool{
		{
			name: "tokenops_glance", title: "Plans at a glance", kind: reads,
			description: "Every subscription plan's pressure in one call: the vendor's windows with their pace (whether each lasts to its reset or when it runs out), spend against limits, the session budget, and the coach's findings. Call it before a long task. It never changes a model or applies anything; it says uncertain when the evidence is thin.",
			selector:    "view", def: "all", input: glanceIn{}, output: glanceOut{},
			routes: map[string]route{
				"all":            {inner: "tokenops_resource_glance"},
				"headroom":       {inner: "tokenops_plan_headroom"},
				"session_budget": {inner: "tokenops_session_budget"},
				"findings":       {inner: "tokenops_findings"},
			},
		},
		{
			name: "tokenops_spend", title: "Spend", kind: reads,
			description: "What was spent and where it went: totals over a window, the top consumers, the burn over recent hours, or a forecast. On a flat-rate plan billed cost is about zero, so read the API-equivalent value and tokens.",
			selector:    "view", def: "summary", input: spendIn{}, output: spendOut{},
			routes: map[string]route{
				"summary":  {inner: "tokenops_spend_summary", params: params("since", "until", "workflow_id", "agent_id", "include_sources")},
				"top":      {inner: "tokenops_top_consumers", params: params("by", "top", "since", "until", "include_sources")},
				"burn":     {inner: "tokenops_burn_rate", params: params("hours", "include_sources")},
				"forecast": {inner: "tokenops_forecast", params: params("horizon_days", "include_sources")},
			},
		},
		{
			name: "tokenops_sessions", title: "How sessions go", kind: reads,
			description: "What agent sessions are like to work with, from the transcripts the clients write: graded turns, rework, interrupts and the one change worth making; the recent work task by task; or the operator's prompting scored. Derived figures only; quoted instructions are withheld.",
			selector:    "view", def: "dx", input: sessionsIn{}, output: sessionsOut{},
			routes: map[string]route{
				"dx":      {inner: "tokenops_agent_dx", params: params("days", "all")},
				"story":   {inner: "tokenops_story", params: params("days", "all", "limit")},
				"prompts": {inner: "tokenops_coach_prompts", params: params("since", "until", "session_id", "limit")},
			},
		},
		{
			name: "tokenops_status", title: "TokenOps status", kind: reads,
			description: "Whether TokenOps is working and how it is set up: readiness with blockers and next actions, the operating mode, the configuration (secrets redacted), each data source's health, the usage readers, or this server's version.",
			selector:    "view", def: "status", input: statusIn{}, output: statusOut{},
			routes: map[string]route{
				"status":       {inner: "tokenops_status"},
				"mode":         {inner: "tokenops_mode"},
				"config":       {inner: "tokenops_config"},
				"data_sources": {inner: "tokenops_data_sources", params: window},
				"vendor_usage": {inner: "tokenops_vendor_usage_status", params: params("window_hours")},
				"version":      {inner: "tokenops_version"},
			},
		},
		{
			name: "tokenops_explain", title: "Explain", kind: reads,
			description: "What a figure means — what it measures, how, and how to read it — or, given a decision ID, why TokenOps decided what it did: the evidence, the alternatives, the policy and the result.",
			input:       explainIn{}, output: explainOut{},
			pick: func(args map[string]any) string {
				if id, _ := args["decision_id"].(string); id != "" {
					return "decision"
				}
				return "term"
			},
			routes: map[string]route{
				"term":     {inner: "tokenops_explain", params: params("term")},
				"decision": {inner: "tokenops_explain_decision", params: params("decision_id")},
			},
		},
		{
			name: "tokenops_records", title: "Records", kind: reads,
			description: "What TokenOps recorded: optimizations, the audit log, domain event counts, one workflow's trace with its waste findings, the scorecard, or a comparison of work with and without optimizations.",
			selector:    "view", def: "optimizations", input: recordsIn{}, output: recordsOut{},
			routes: map[string]route{
				"optimizations": {inner: "tokenops_optimizations", params: params("since", "until", "workflow_id", "agent_id", "limit")},
				"audit":         {inner: "tokenops_audit", params: params("action", "actor", "since", "until", "limit")},
				"events":        {inner: "tokenops_domain_events"},
				"workflow":      {inner: "tokenops_workflow_trace", params: params("workflow_id")},
				"scorecard":     {inner: "tokenops_scorecard", params: params("since_days", "fvt_seconds", "teu_pct", "sac_pct", "baseline_ref")},
				"verify":        {inner: "tokenops_verify", params: params("days", "experiment_id")},
			},
		},
		{
			name: "tokenops_rules", title: "Agent rules", kind: reads,
			description: "The repository's agent rules files (CLAUDE.md, AGENTS.md and the like): their size and quality, conflicts and repeats, a compressed set that keeps quality, or the rules relevant to one piece of work within a token budget. It reads; it writes no file.",
			selector:    "view", def: "analyze", input: rulesIn{}, output: rulesOut{},
			routes: map[string]route{
				"analyze":   {inner: "tokenops_rules_analyze", params: params("root", "repo_id", "provider")},
				"conflicts": {inner: "tokenops_rules_conflicts", params: params("root", "repo_id")},
				"compress":  {inner: "tokenops_rules_compress", params: params("root", "repo_id", "similarity_threshold", "quality_floor")},
				"inject":    {inner: "tokenops_rules_inject", params: params("root", "repo_id", "workflow_id", "agent_id", "files", "tools", "keywords", "token_budget", "min_score", "include_global")},
			},
		},
		{
			name: "tokenops_fmt", title: "Command output compression", kind: reads,
			description: "How `tokenops fmt` is doing at compressing command output before the agent reads it: which commands to compress next and any over-compression, or how much output in past sessions could be compressed.",
			selector:    "view", def: "learn", input: fmtIn{}, output: fmtOut{},
			routes: map[string]route{
				"learn":   {inner: "tokenops_fmt_learn", params: params("recover_dir", "no_jsonl", "limit")},
				"analyze": {inner: "tokenops_fmt_analyze", params: params("root", "max_files")},
			},
		},
		{
			name: "tokenops_pricing", title: "Model prices", kind: reads,
			description: "Model prices from the rate card TokenOps prices with, per million tokens: input, cached input and output. Filter by provider or model.",
			input:       pricingInput{}, output: pricingResult{},
			routes: map[string]route{"": {inner: "tokenops_pricing", params: params("provider", "model", "limit")}},
		},
		{
			name: "tokenops_prepare_work", title: "Prepare work", kind: records,
			description: "Before starting a task: plan headroom and a model recommendation for the instruction, and a workflow_id to pass to tokenops_review_work afterwards (and as X-Tokenops-Workflow-Id where requests carry headers). It records the advice; it never changes the caller's model.",
			input:       routingAdviceInput{}, output: prepareWorkResult{},
			routes: map[string]route{"": {inner: "tokenops_prepare_work", params: params("instruction", "provider", "model", "tool_density", "work_id", "execution_id", "actor_id", "workflow_id")}},
		},
		{
			name: "tokenops_review_work", title: "Review work", kind: reads,
			description: "After a task: one workflow's steps, context growth, token and cost totals and coaching findings, keeping measured evidence apart from no evidence. Pass the workflow_id from tokenops_prepare_work. Aggregates only, never prompt content.",
			input:       reviewWorkInput{}, output: reviewWorkResult{},
			routes: map[string]route{"": {inner: "tokenops_review_work", params: params("workflow_id")}},
		},
		{
			name: "tokenops_outcome", title: "Record an outcome", kind: records,
			description: "Record how a piece of work turned out — the operator's assessment, or the local verifier result after the session's last edit — so TokenOps learns which decisions paid off.",
			selector:    "action", def: "record", input: outcomeIn{}, output: outcomeOut{},
			routes: map[string]route{
				"record": {inner: "tokenops_outcome_record", params: params("execution_id", "decision_id", "result", "caveat", "attention_minutes")},
				"detect": {inner: "tokenops_outcome_detect", params: params("execution_id", "decision_id", "session_id")},
			},
		},
		{
			name: "tokenops_routing", title: "Model routing", kind: changes,
			description: "Which model a turn should run on, and the routing the operator governs: advice from measured signal (it only suggests, and only routes down for mechanical work while a window is tight), upgrades waiting on the operator, approving or denying one, and setting or removing a routing rule.",
			selector:    "action", def: "advise", input: routingIn{}, output: routingOut{},
			routes: map[string]route{
				"advise":    {inner: "tokenops_routing_advise", params: params("instruction", "provider", "model", "tool_density", "work_id", "execution_id", "actor_id", "workflow_id")},
				"proposals": {inner: "tokenops_routing_proposals"},
				"decide":    {inner: "tokenops_routing_decide", params: params("key", "decision")},
				"set_rule":  {inner: "tokenops_routing_rule_set", params: params("provider", "from_model", "to_model", "quality", "fallbacks", "delete")},
			},
		},
		{
			name: "tokenops_configure", title: "Configure TokenOps", kind: changes,
			description: "Change TokenOps' settings: bind a subscription plan, set or remove a budget, switch the operating mode, cap the model a provider may be routed to, or connect claude.ai's usage meter. Each change is written to the config and the audit log, and the daemon reloads it.",
			selector:    "setting", input: configureIn{}, output: configureOut{},
			routes: map[string]route{
				"plan":            {inner: "tokenops_plan_set", params: params("provider", "plan", "spend_limit_usd", "limit_window", "rate_factor", "clear", "price", "currency", "since")},
				"budget":          {inner: "tokenops_budget_set", params: params("name", "window", "limit_usd", "limit_tokens", "warn_at", "crit_at", "workflow_id", "agent_id", "basis", "delete")},
				"mode":            {inner: "tokenops_mode", params: params("mode"), rename: map[string]string{"mode": "set"}},
				"preferred_model": {inner: "tokenops_preferred_model", params: params("provider", "model", "clear")},
				"usage_meter":     {inner: "tokenops_vendor_usage_setup", params: params("org")},
			},
		},
		{
			name: "tokenops_coach", title: "Coach", kind: changes,
			description: "Show or change how much the coach says and does: a preset (observe, advise, guided, autopilot), each power's autonomy, and its verbosity. With no arguments it reports the current settings and what became of its advice.",
			input:       coachInput{}, output: coachcap.Report{},
			routes: map[string]route{"": {inner: "tokenops_coach", params: params("preset", "autonomy", "power", "rung", "verbosity")}},
		},
		{
			name: "tokenops_experiment", title: "Routing experiment", kind: changes,
			description: "Run a bounded, opt-in trial of a routing change: start it with its guardrails, check its state, or stop it. Evidence is kept when it stops.",
			input:       experimentInput{}, output: experimentResult{},
			routes: map[string]route{"": {inner: "tokenops_experiment", params: params("action", "baseline_model", "duration_days", "experiment_id", "guardrails", "max_pairs", "min_improvement_pct")}},
		},
	}
}

// PublicRoutes names, for each public tool, the registered tools its views
// answer from — what a check of every question an agent can ask walks.
func PublicRoutes() map[string][]string {
	out := map[string][]string{}
	for _, p := range publicTools() {
		for _, name := range p.routeNames() {
			out[p.name] = append(out[p.name], p.routes[name].inner)
		}
	}
	return out
}
