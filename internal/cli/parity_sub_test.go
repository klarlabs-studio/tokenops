package cli

import (
	"strings"
	"testing"
)

// subToMCP maps each subcommand to the tool(s) that do the same thing.
//
// The top-level check reads `plan` as covered because tokenops_plan_headroom
// exists, while `plan set` had no agent equivalent at all — a Claude
// Enterprise user could read headroom from an agent but not bind the plan
// it measures. The comparison has to happen where the verbs are.
var subToMCP = map[string][]string{
	"budget set":            {"tokenops_budget_set"},
	"budget unset":          {"tokenops_budget_set"},
	"coach prompts":         {"tokenops_coach_prompts"},
	"config show":           {"tokenops_config"},
	"decision explain":      {"tokenops_explain_decision"},
	"experiment start":      {"tokenops_experiment"},
	"experiment status":     {"tokenops_experiment"},
	"experiment stop":       {"tokenops_experiment"},
	"fmt analyze":           {"tokenops_fmt_analyze"},
	"fmt learn":             {"tokenops_fmt_learn"},
	"plan headroom":         {"tokenops_plan_headroom"},
	"plan list":             {"tokenops_plan_set"},
	"plan set":              {"tokenops_plan_set"},
	"plan unset":            {"tokenops_plan_set"},
	"outcome record":        {"tokenops_outcome_record"},
	"outcome detect":        {"tokenops_outcome_detect"},
	"preferred-model list":  {"tokenops_preferred_model"},
	"preferred-model set":   {"tokenops_preferred_model"},
	"preferred-model unset": {"tokenops_preferred_model"},
	"pricing show":          {"tokenops_pricing"},
	"routing proposals":     {"tokenops_routing_proposals", "tokenops_routing_decide"},
	"routing rule":          {"tokenops_routing_rule_set"},
	"rules analyze":         {"tokenops_rules_analyze"},
	"rules bench":           {"tokenops_rules_bench"},
	"rules compress":        {"tokenops_rules_compress"},
	"rules conflicts":       {"tokenops_rules_conflicts"},
	"rules inject":          {"tokenops_rules_inject"},
	"vendor-usage setup":    {"tokenops_vendor_usage_setup"},
	"vendor-usage status":   {"tokenops_vendor_usage_status"},
}

// subCLIOnly are subcommands with no tool, each with the reason. Entries
// starting "GAP:" are not deliberate — they are recorded so the asymmetry
// is visible, and closing one means moving it to subToMCP.
var subCLIOnly = map[string]string{
	"dashboard rotate-token": "rotates a local secret the dashboard authenticates with",
	"fmt hook":               "is a hook entry point invoked by a client, not a user",
	"fmt recover":            "prints a stored full output by recovery id, which the agent already receives inline",
	"fmt bench":              "measures formatters over a corpus of captured outputs — a contributor tool",
	"pricing refresh":        "fetches and snapshots the rate card — catalog maintenance, not a question",
	"pricing diff":           "diffs rate-card snapshots — catalog maintenance",
	"pricing lint":           "lints rate-card snapshots — catalog maintenance",
	"budget list":            "GAP: tokenops_budget_set writes budgets but cannot list them; tokenops_config shows them",
	"plan catalog":           "GAP: no tool lists the plan catalog; an invalid name in tokenops_plan_set is refused with the reason",
	"coach delivery":         "GAP: coaching delivery (observe/advise/intervene) is not settable from an agent",
	"coach replies":          "GAP: reply-compression detection has no tool",
	"task start":             "GAP: marking a task boundary has no tool; tokenops_story reads inferred tasks",
	"task done":              "GAP: marking a task boundary has no tool",
	"task list":              "GAP: tokenops_story reads tasks back, but not this recorded list",
	"vendor-usage enable":    "GAP: sources other than the Claude usage meter cannot be enabled from an agent",
	"vendor-usage backfill":  "GAP: backfill has no tool",
}

func TestCLISubcommandParity(t *testing.T) {
	tools := mcpToolNames(t)
	for _, parent := range NewRoot().Commands() {
		if _, local := cliOnly[parent.Name()]; local {
			continue
		}
		for _, sub := range parent.Commands() {
			name := parent.Name() + " " + sub.Name()
			if reason, ok := subCLIOnly[name]; ok {
				if strings.TrimSpace(reason) == "" {
					t.Errorf("%q is CLI-only without a reason", name)
				}
				continue
			}
			mapped, ok := subToMCP[name]
			if !ok {
				t.Errorf("subcommand %q has no MCP mapping and is not in subCLIOnly.\n"+
					"  Map it to the tool that does the same thing, or record why it is CLI-only.", name)
				continue
			}
			for _, want := range mapped {
				if !tools[want] {
					t.Errorf("%q maps to %q, which does not exist", name, want)
				}
			}
		}
	}
	// Stale entries rot into a list nobody trusts.
	live := map[string]bool{}
	for _, parent := range NewRoot().Commands() {
		for _, sub := range parent.Commands() {
			live[parent.Name()+" "+sub.Name()] = true
		}
	}
	for name := range subToMCP {
		if !live[name] {
			t.Errorf("subToMCP lists %q, which is no longer a command", name)
		}
	}
	for name := range subCLIOnly {
		if !live[name] {
			t.Errorf("subCLIOnly lists %q, which is no longer a command", name)
		}
	}
}
