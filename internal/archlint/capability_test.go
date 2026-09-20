package archlint

import (
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// The hole the capability layer closes.
//
// archlint has always constrained domain → adapter and domain → infra.
// It places no constraint the other way, and that is the gap the diverged
// capabilities grew through: internal/cli (~17.8k LOC) and internal/mcp
// (~8.8k LOC) each reach straight into the domain and each implement
// their own validation, orchestration and formatting on top of it.
//
// The result was not merely duplication, it was divergence:
//
//   - plan headroom — the MCP tool sorted providers, the CLI ranged the
//     map, so "the first plan" was a different plan from run to run on
//     one side. The fix was written once, in one of the two places that
//     had the bug.
//   - agent dx — the CLI reads everything at --days 0, the MCP tool needs
//     all:true; the CLI can exclude scratch directories, the tool cannot
//     express it; the CLI warns and renders on an extraction error, the
//     tool fails the call.
//   - status — two unrelated implementations, two structs both named
//     statusResult.
//   - routing decide — MCP only, its store-opening logic written three
//     times.
//
// internal/cli/parity_test.go guards that every command has a matching
// tool and vice versa, and it passed through all of it. Name parity is
// not capability parity.
//
// A hard rule cannot land today: migrating every capability at once is
// the rewrite ADR 0004 lists as a non-goal. What lands is a ratchet. The
// baseline below records every direct adapter → domain import that exists
// now; the test fails on anything new, and fails on anything stale, so
// the list can only shrink and must be kept honest as it does.

// adapters are the packages this rule governs.
var adapters = []string{
	"go.klarlabs.de/tokenops/internal/cli",
	"go.klarlabs.de/tokenops/internal/mcp",
	"go.klarlabs.de/tokenops/internal/proxy",
}

// capabilityLayer is where an adapter should be reaching instead.
const capabilityLayer = "go.klarlabs.de/tokenops/internal/capability/"

// domainPrefix identifies a domain package.
const domainPrefix = "go.klarlabs.de/tokenops/internal/contexts/"

// directDomainImports is the ratchet: every direct adapter → domain
// import that exists today.
//
// **This list may only shrink.** Adding an entry means a new capability
// was implemented inside an adapter instead of in internal/capability,
// which is the thing that produced four divergent implementations. If a
// migration removes the last use of a package, delete its line — the
// test fails on a stale entry too, so the list stays an accurate measure
// of how much is left.
//
// It has already done both jobs. Rebasing this branch onto the earlier
// phases, the test refused the merge until the list was refreshed: the
// fmt recovery store's redaction added internal/cli → security/redaction,
// /api/sources added proxy and mcp → observability/freshness, and moving
// the origin probes to internal/infra/sourceprobe removed two entries
// from internal/cli. Four movements, none of which anyone would have
// noticed by hand.
var directDomainImports = map[string][]string{
	"go.klarlabs.de/tokenops/internal/cli": {
		"go.klarlabs.de/tokenops/internal/contexts/coaching/prompts",
		"go.klarlabs.de/tokenops/internal/contexts/coaching/replies",
		"go.klarlabs.de/tokenops/internal/contexts/coaching/tools",
		"go.klarlabs.de/tokenops/internal/contexts/coaching/waste",
		"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx",
		"go.klarlabs.de/tokenops/internal/contexts/governance/coverdebt",
		"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard",
		"go.klarlabs.de/tokenops/internal/contexts/governance/story",
		"go.klarlabs.de/tokenops/internal/contexts/observability/analytics",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/eval",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/fmtlearn",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/replay",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass",
		"go.klarlabs.de/tokenops/internal/contexts/prompts/providers",
		"go.klarlabs.de/tokenops/internal/contexts/rules",
		"go.klarlabs.de/tokenops/internal/contexts/security/audit",
		"go.klarlabs.de/tokenops/internal/contexts/security/redaction",
		"go.klarlabs.de/tokenops/internal/contexts/spend/forecast",
		"go.klarlabs.de/tokenops/internal/contexts/spend/plans",
		"go.klarlabs.de/tokenops/internal/contexts/spend/pricing",
		"go.klarlabs.de/tokenops/internal/contexts/spend/session",
		"go.klarlabs.de/tokenops/internal/contexts/spend/spend",
		"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic",
		"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter",
		"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode",
		"go.klarlabs.de/tokenops/internal/contexts/tasks",
		"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow",
	},
	"go.klarlabs.de/tokenops/internal/mcp": {
		"go.klarlabs.de/tokenops/internal/contexts/coaching/prompts",
		"go.klarlabs.de/tokenops/internal/contexts/coaching/waste",
		"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx",
		"go.klarlabs.de/tokenops/internal/contexts/governance/coverdebt",
		"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard",
		"go.klarlabs.de/tokenops/internal/contexts/governance/story",
		"go.klarlabs.de/tokenops/internal/contexts/observability/analytics",
		"go.klarlabs.de/tokenops/internal/contexts/observability/freshness",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/eval",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/fmtlearn",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/replay",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval",
		"go.klarlabs.de/tokenops/internal/contexts/rules",
		"go.klarlabs.de/tokenops/internal/contexts/security/audit",
		"go.klarlabs.de/tokenops/internal/contexts/spend/forecast",
		"go.klarlabs.de/tokenops/internal/contexts/spend/plans",
		"go.klarlabs.de/tokenops/internal/contexts/spend/pricing",
		"go.klarlabs.de/tokenops/internal/contexts/spend/session",
		"go.klarlabs.de/tokenops/internal/contexts/spend/spend",
		"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter",
		"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow",
	},
	"go.klarlabs.de/tokenops/internal/proxy": {
		"go.klarlabs.de/tokenops/internal/contexts/coaching/waste",
		"go.klarlabs.de/tokenops/internal/contexts/observability/analytics",
		"go.klarlabs.de/tokenops/internal/contexts/observability/freshness",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer",
		"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router",
		"go.klarlabs.de/tokenops/internal/contexts/prompts/providers",
		"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer",
		"go.klarlabs.de/tokenops/internal/contexts/rules",
		"go.klarlabs.de/tokenops/internal/contexts/security/audit",
		"go.klarlabs.de/tokenops/internal/contexts/spend/forecast",
		"go.klarlabs.de/tokenops/internal/contexts/spend/spend",
		"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow",
	},
}

// TestAdapterDomainImportsOnlyShrink compares the recorded baseline to
// what the adapters actually import.
func TestAdapterDomainImportsOnlyShrink(t *testing.T) {
	for _, adapter := range adapters {
		t.Run(short(adapter), func(t *testing.T) {
			actual := domainImportsOf(t, adapter)
			recorded := directDomainImports[adapter]

			added := missing(actual, recorded)
			if len(added) > 0 {
				t.Errorf("new direct domain imports:\n  %s\n\n"+
					"An adapter reaching a new domain package means a capability is being "+
					"implemented in %s rather than under %s, which is how the CLI and the "+
					"MCP server came to disagree about plan headroom, agent dx, status and "+
					"routing. Put the use case in a capability package and call it from both.",
					strings.Join(added, "\n  "), short(adapter), capabilityLayer)
			}

			gone := missing(recorded, actual)
			if len(gone) > 0 {
				t.Errorf("these are recorded but no longer imported — delete them from "+
					"directDomainImports[%q]:\n  %s\n\n"+
					"The list is a measure of how much is left to migrate, and a stale "+
					"entry overstates it.", short(adapter), strings.Join(gone, "\n  "))
			}
		})
	}
}

// TestCapabilityPackagesAreReachable fails if a capability package
// exists that no adapter calls. A capability nobody uses is a third
// implementation, not a shared one.
func TestCapabilityPackagesAreReachable(t *testing.T) {
	caps := packagesUnder(t, capabilityLayer+"...")
	if len(caps) == 0 {
		t.Skip("no capability packages yet")
	}
	used := map[string]bool{}
	for _, adapter := range adapters {
		for _, imp := range importsOf(t, adapter) {
			if strings.HasPrefix(imp, capabilityLayer) {
				used[imp] = true
			}
		}
	}
	for _, c := range caps {
		if !used[c] {
			t.Errorf("%s is imported by no adapter; a capability nobody calls is a third "+
				"implementation, not a shared one", c)
		}
	}
}

// TestPrintAdapterDomainImports prints the baseline in the literal form
// directDomainImports takes. Run it with -v after a migration to refresh
// the list.
func TestPrintAdapterDomainImports(t *testing.T) {
	for _, adapter := range adapters {
		got := domainImportsOf(t, adapter)
		t.Logf("%q: {", adapter)
		for _, imp := range got {
			t.Logf("\t%q,", imp)
		}
		t.Logf("},")
	}
}

// domainImportsOf returns the domain packages an adapter imports
// directly, sorted.
func domainImportsOf(t *testing.T, pkg string) []string {
	t.Helper()
	var out []string
	for _, imp := range importsOf(t, pkg) {
		if strings.HasPrefix(imp, domainPrefix) {
			out = append(out, imp)
		}
	}
	sort.Strings(out)
	return out
}

func importsOf(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", "{{range .Imports}}{{println .}}{{end}}", pkg).Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pkg, err)
	}
	return nonEmptyLines(string(out))
}

func packagesUnder(t *testing.T, pattern string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", pattern).Output()
	if err != nil {
		// No packages match yet, which is not a failure.
		return nil
	}
	return nonEmptyLines(string(out))
}

func nonEmptyLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// missing returns the entries of a that are not in b.
func missing(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, s := range b {
		in[s] = true
	}
	var out []string
	for _, s := range a {
		if !in[s] {
			out = append(out, s)
		}
	}
	return out
}

func short(pkg string) string {
	return strings.TrimPrefix(pkg, "go.klarlabs.de/tokenops/")
}
