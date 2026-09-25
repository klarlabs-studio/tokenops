package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/experiments"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/mcp"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// cliOnly are commands that deliberately have no MCP tool, each with the
// reason. They are local acts, not questions: an agent asking the server to
// install hooks on the machine it is running on, or to start the daemon that
// hosts it, is a different proposition from asking it what spend looks like.
var cliOnly = map[string]string{
	"init":        "writes config and wires this machine's clients",
	"detect":      "reads this machine's filesystem to report installed clients",
	"start":       "runs the ingestion daemon in the foreground",
	"daemon":      "installs, restarts and removes a supervisor unit",
	"serve":       "is the MCP server itself",
	"hooks":       "edits client hook configuration on this machine",
	"coach-hook":  "is a hook entry point invoked by a client, not a user",
	"read-guard":  "is a hook entry point invoked by a client, not a user",
	"route-guard": "is a hook entry point invoked by a client, not a user",
	"completion":  "is cobra's shell completion generator",
	"help":        "is cobra's help command",
	"provider":    "binds upstream base URLs, which only the daemon's proxy reads",
}

// cliToMCP maps a CLI command to the tool(s) that answer the same question.
// A command absent from both this map and cliOnly fails the test: that is a
// surface that grew on one side only, which is how `detect`, `daemon
// restart` and `vendor-usage setup` all shipped CLI-only without anything
// noticing.
var cliToMCP = map[string][]string{
	"audit":           {"tokenops_audit"},
	"coach":           {"tokenops_coach_prompts"},
	"config":          {"tokenops_config"},
	"coverage-debt":   {"tokenops_coverage_debt"},
	"dx":              {"tokenops_agent_dx"},
	"eval":            {"tokenops_eval"},
	"events":          {"tokenops_domain_events"},
	"fmt":             {"tokenops_fmt_analyze", "tokenops_fmt_learn"},
	"plan":            {"tokenops_plan_headroom", "tokenops_plan_set"},
	"replay":          {"tokenops_replay"},
	"rules":           {"tokenops_rules_analyze", "tokenops_rules_bench", "tokenops_rules_compress", "tokenops_rules_conflicts", "tokenops_rules_inject"},
	"scorecard":       {"tokenops_scorecard"},
	"spend":           {"tokenops_spend_summary", "tokenops_top_consumers", "tokenops_burn_rate", "tokenops_forecast"},
	"status":          {"tokenops_status"},
	"story":           {"tokenops_story"},
	"verify":          {"tokenops_verify"},
	"decision":        {"tokenops_explain_decision"},
	"outcome":         {"tokenops_outcome_record", "tokenops_outcome_detect"},
	"experiment":      {"tokenops_experiment"},
	"task":            {"tokenops_workflow_trace"},
	"version":         {"tokenops_version"},
	"pricing":         {"tokenops_pricing"},
	"vendor-usage":    {"tokenops_vendor_usage_status", "tokenops_vendor_usage_setup"},
	"mode":            {"tokenops_mode"},
	"preferred-model": {"tokenops_preferred_model"},
	"budget":          {"tokenops_budget_set"},
	"optimizations":   {"tokenops_optimizations"},
	"routing":         {"tokenops_routing_proposals", "tokenops_routing_rule_set"},
}

// mcpOnly are tools with no CLI equivalent, each with the reason. Several
// are not deliberate — they are recorded here so the asymmetry is visible
// rather than discovered, and the comment says which is which.
var mcpOnly = map[string]string{
	"tokenops_help":            "indexes the tool surface for an agent that cannot read --help",
	"tokenops_data_sources":    "reports event counts by source; `vendor-usage status` is the CLI's fuller answer",
	"tokenops_session_budget":  "per-turn advice for the agent mid-session; no terminal equivalent makes sense",
	"tokenops_routing_advise":  "asks which model a turn should run on; the CLI equivalent is the route-guard hook",
	"tokenops_routing_decide":  "same, for an explicit decision",
	"tokenops_review_work":     "composes workflow measurement and coaching into one agent-oriented review",
	"tokenops_prepare_work":    "composes current plan headroom and per-task model advice before execution",
	"tokenops_resource_glance": "composes session budget and plan headroom into a caveated resource-pressure summary",
}

// TestCLIAndMCPParity diffs the two surfaces.
//
// It is not an assertion that they should be identical — several
// asymmetries are correct, and the allowlists carry the reason for each.
// What it prevents is an asymmetry appearing without anyone deciding on it.
func TestCLIAndMCPParity(t *testing.T) {
	cliCmds := cliCommandNames(t)
	tools := mcpToolNames(t)

	for _, name := range cliCmds {
		if _, ok := cliOnly[name]; ok {
			continue
		}
		mapped, ok := cliToMCP[name]
		if !ok {
			t.Errorf("CLI command %q has no MCP mapping and is not in cliOnly.\n"+
				"  Add it to cliToMCP with the tool that answers the same question, "+
				"or to cliOnly with the reason it is local-only.", name)
			continue
		}
		for _, want := range mapped {
			if !tools[want] {
				t.Errorf("CLI %q maps to %q, which no longer exists", name, want)
			}
		}
	}

	covered := map[string]bool{}
	for _, ts := range cliToMCP {
		for _, tname := range ts {
			covered[tname] = true
		}
	}
	for tool := range tools {
		if covered[tool] {
			continue
		}
		if _, ok := mcpOnly[tool]; ok {
			continue
		}
		t.Errorf("MCP tool %q has no CLI equivalent and is not in mcpOnly.\n"+
			"  Add the CLI command to cliToMCP, or record the asymmetry in mcpOnly with its reason.", tool)
	}
}

// TestEveryToolGroupIsWiredIntoServe catches the other direction: a
// registration function written and never called, so its tools exist in the
// package and reach no client.
func TestEveryToolGroupIsWiredIntoServe(t *testing.T) {
	serveSrc, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatalf("read serve.go: %v", err)
	}
	defined := registerFuncsInMCP(t)
	if len(defined) == 0 {
		t.Fatal("found no Register* functions in the mcp package")
	}
	for _, fn := range defined {
		if !strings.Contains(string(serveSrc), "mcp."+fn+"(") {
			t.Errorf("mcp.%s is never called from serve.go — its tools reach no client", fn)
		}
	}
}

// TestParityTestRegistersEveryToolGroup closes a hole in the check above.
//
// mcpToolNames builds its own server, so a tool group the package defines
// and this file forgets to register is invisible to the parity diff — the
// tools exist, reach clients through serve, and are never compared to
// anything. That happened immediately: two tools were added and the parity
// test passed unchanged, because it did not know to register them.
func TestParityTestRegistersEveryToolGroup(t *testing.T) {
	src, err := os.ReadFile("parity_test.go")
	if err != nil {
		t.Fatalf("read parity_test.go: %v", err)
	}
	for _, fn := range registerFuncsInMCP(t) {
		if !strings.Contains(string(src), "mcp."+fn+"(") {
			t.Errorf("mcp.%s is not registered in mcpToolNames, so its tools are absent from the "+
				"parity diff. Add it there as well as to serve.go.", fn)
		}
	}
}

func cliCommandNames(t *testing.T) []string {
	t.Helper()
	cmds := NewRoot().Commands()
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c.Name())
	}
	sort.Strings(out)
	return out
}

// mcpToolNames builds a server the way serve does and asks it what it has.
// Runtime rather than a source scan, so a tool registered only when its
// dependencies are present is counted the same way a client would see it.
func mcpToolNames(t *testing.T) map[string]bool {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "p.db"), sqlite.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv := mcp.NewServer("tokenops", "test", nil)
	eng := spend.NewEngine(spend.DefaultTable())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	must(mcp.RegisterTools(srv, mcp.Deps{Store: store, Spend: eng, Aggregator: analytics.New(store, eng)}))
	must(mcp.RegisterRulesTools(srv))
	must(mcp.RegisterParityTools(srv, mcp.ParityDeps{Store: store, Spend: eng}))
	must(mcp.RegisterControlTools(srv, mcp.ControlDeps{}))
	must(mcp.RegisterPlanTools(srv, mcp.PlanDeps{}))
	must(mcp.RegisterAgentDXTools(srv, mcp.AgentDXDeps{}))
	must(mcp.RegisterStoryTools(srv, mcp.StoryDeps{}))
	must(mcp.RegisterVerifyTool(srv, mcp.VerifyDeps{}))
	must(mcp.RegisterOutcomeTools(srv, mcp.OutcomeDeps{Store: store}))
	must(mcp.RegisterDecisionTools(srv, mcp.DecisionDeps{Store: store}))
	must(mcp.RegisterExperimentTools(srv, mcp.ExperimentDeps{Manager: experiments.New(store)}))
	must(mcp.RegisterRoutingAdviceTools(srv, mcp.RoutingAdviceDeps{}))
	must(mcp.RegisterApprovalTools(srv, mcp.ApprovalDeps{}))
	must(mcp.RegisterModeTools(srv, mcp.ModeDeps{}))
	must(mcp.RegisterHelpTool(srv))
	must(mcp.RegisterDataSourcesTool(srv, mcp.DataSourcesDeps{Store: store}))
	must(mcp.RegisterFmtTools(srv))
	must(mcp.RegisterCoachTools(srv, mcp.CoachDeps{}))
	must(mcp.RegisterGapTools(srv, mcp.GapDeps{}))
	must(mcp.RegisterSetupTools(srv, mcp.SetupDeps{}))

	out := map[string]bool{}
	for _, ti := range srv.Tools() {
		out[ti.Name] = true
	}
	return out
}

var registerRe = regexp.MustCompile(`(?m)^func (Register[A-Za-z]+)\(`)

func registerFuncsInMCP(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("../mcp")
	if err != nil {
		t.Fatalf("read mcp dir: %v", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join("../mcp", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range registerRe.FindAllStringSubmatch(string(b), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	sort.Strings(out)
	return out
}

var _ = cobra.Command{}
