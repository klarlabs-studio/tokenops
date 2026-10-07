// Package archlint enforces the DDD layering rules documented in
// docs/architecture-ddd.md. The test below uses `go list -deps` to
// confirm that domain packages do not transitively import
// infrastructure or adapter types whose presence would break the
// layering contract. PRs that violate the rule fail CI rather than
// silently rotting the architecture.
package archlint

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// forbiddenAdapters lists adapter packages no domain package may
// depend on directly or transitively.
var forbiddenAdapters = []string{
	"go.klarlabs.de/tokenops/internal/proxy",
	"go.klarlabs.de/tokenops/internal/cli",
	"go.klarlabs.de/tokenops/internal/mcp",
}

// forbiddenInfra lists infrastructure packages domain packages must
// not import directly. analytics is the contracted read-side
// abstraction (its Store port is implemented by *sqlite.Store), so
// packages that depend on analytics.Row (forecast, spend) are still
// allowed; they must NOT import sqlite themselves.
var forbiddenInfra = []string{
	"go.klarlabs.de/tokenops/internal/storage/sqlite",
}

// storageExempt domains legitimately import sqlite via an isolated
// adapter file. Only packages that actually import
// internal/storage/sqlite belong here — TestStorageExemptImportsSQLite
// fails the build if an exemption is stale. Documented in
// docs/architecture-ddd.md.
var storageExempt = map[string]bool{
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard": true,
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow":   true,
	"go.klarlabs.de/tokenops/internal/contexts/optimization/replay":  true,
	"go.klarlabs.de/tokenops/internal/contexts/telemetry/retention":  true,
	"go.klarlabs.de/tokenops/internal/contexts/tasks":                true,
}

// forbiddenOuterPrefixes names the outer layers no domain package may
// import directly: the daemon and its composition root, configuration,
// presentation, and every infrastructure adapter under internal/infra/.
// An entry ending in "/" matches every package beneath it; any other
// entry matches the package itself and its sub-packages.
//
// internal/events is deliberately absent. It is the event-bus port the
// domains publish to: the domain code uses only its Bus and Observable
// interfaces, and the package imports nothing from this module except
// pkg/eventschema (TestEventsPackageStaysAPort holds it to that). Moving
// those two interfaces elsewhere would churn 16 packages to relabel a
// port that is already infrastructure-free.
var forbiddenOuterPrefixes = []string{
	"go.klarlabs.de/tokenops/internal/daemon",
	"go.klarlabs.de/tokenops/internal/config",
	"go.klarlabs.de/tokenops/internal/bootstrap",
	"go.klarlabs.de/tokenops/internal/presentation",
	"go.klarlabs.de/tokenops/internal/infra/",
}

// outerImportExempt is the ratchet for direct domain → outer-layer
// imports that already existed when forbiddenOuterPrefixes landed.
//
// **This list may only shrink.** A new entry means a domain package now
// depends on an adapter; define a port in the domain instead and let the
// adapter satisfy it. When a dependency is removed, delete its line —
// TestOuterImportExemptNotStale fails on a stale entry, so the list stays
// an accurate count of what is left to invert.
var outerImportExempt = map[string][]string{
	// Skips scratch-directory transcripts via scanscope.EphemeralPath.
	"go.klarlabs.de/tokenops/internal/contexts/coaching/prompts": {
		"go.klarlabs.de/tokenops/internal/infra/scanscope",
	},
	// Narrows its transcript scan via scanscope.Keep.
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx": {
		"go.klarlabs.de/tokenops/internal/infra/scanscope",
	},
	// The poller reads, and builds envelopes from, claudelimits.Reading.
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudestatusline": {
		"go.klarlabs.de/tokenops/internal/infra/claudelimits",
	},
	// The poller reads, and builds envelopes from, cursorturns.Turn.
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursorturns": {
		"go.klarlabs.de/tokenops/internal/infra/cursorturns",
	},
}

// forbiddenIOImports are the standard-library packages that reach outside
// the process: HTTP, subprocesses and SQL. A domain package importing one
// is an adapter living in the domain tree. The I/O belongs under
// internal/infra (the vendor-usage clients live in internal/infra/vendorusage),
// behind a port the domain declares and the composition root wires.
var forbiddenIOImports = []string{"net/http", "os/exec", "database/sql"}

// ioImportExempt is the ratchet for domain packages that still import one
// of forbiddenIOImports, each with the reason it has not moved yet.
//
// **This list may only shrink.** A new entry means an adapter was written
// inside internal/contexts; put it under internal/infra behind a port
// instead. When a package stops importing one, delete its entry —
// TestIOImportExemptNotStale fails on a stale one.
var ioImportExempt = map[string][]string{
	// Its Cursor reader runs SQL against Cursor's state.vscdb (cursor.go).
	// Moves with an extraction port for agentdx's per-client readers.
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx": {"database/sql"},
	// Reads opencode's SQLite store. Every reader but one goes through the
	// opencodedb.Reader port that internal/infra/opencodedb implements;
	// governance/agentdx (being migrated separately) still calls
	// opencodedb.Read directly. Once it takes the port, the SQL moves into
	// internal/infra/opencodedb and this entry goes.
	"go.klarlabs.de/tokenops/internal/contexts/telemetry/opencodedb": {"database/sql"},
}

// domainPackages lists every domain package the arch test enforces.
// Every package under internal/contexts/* belongs here so new contexts
// are gated automatically — TestDomainPackagesComplete compares this
// list to `go list ./internal/contexts/...`.
var domainPackages = []string{
	"go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough",
	"go.klarlabs.de/tokenops/internal/contexts/coaching/prompts",
	"go.klarlabs.de/tokenops/internal/contexts/coaching/replies",
	"go.klarlabs.de/tokenops/internal/contexts/coaching/tools",
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste",
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget",
	"go.klarlabs.de/tokenops/internal/contexts/governance/modelpolicy",
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx",
	"go.klarlabs.de/tokenops/internal/contexts/governance/story",
	"go.klarlabs.de/tokenops/internal/contexts/governance/coverdebt",
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard",
	"go.klarlabs.de/tokenops/internal/contexts/intervention",
	"go.klarlabs.de/tokenops/internal/contexts/learning",
	"go.klarlabs.de/tokenops/internal/contexts/measurement",
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics",
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness",
	"go.klarlabs.de/tokenops/internal/contexts/observability/observ",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/eval",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/fmtlearn",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/contexttrim",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/dedupe",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/promptcompress",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/qualitygate",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/retrievalprune",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/toolfmt",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/replay",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass",
	"go.klarlabs.de/tokenops/internal/contexts/policy",
	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers",
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer",
	"go.klarlabs.de/tokenops/internal/contexts/rules",
	"go.klarlabs.de/tokenops/internal/contexts/security/audit",
	"go.klarlabs.de/tokenops/internal/contexts/security/dashauth",
	"go.klarlabs.de/tokenops/internal/contexts/security/redaction",
	"go.klarlabs.de/tokenops/internal/contexts/security/tlsmint",
	"go.klarlabs.de/tokenops/internal/contexts/spend/biller",
	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast",
	"go.klarlabs.de/tokenops/internal/contexts/spend/fx",
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans",
	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing",
	"go.klarlabs.de/tokenops/internal/contexts/spend/session",
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudestatusline",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecode",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodeoauth",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexappserver",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/copilot",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/fireworks",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/geminicli",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursor",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursorturns",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/jsonltail",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode",
	"go.klarlabs.de/tokenops/internal/contexts/tasks",
	"go.klarlabs.de/tokenops/internal/contexts/work/fromtasks",
	"go.klarlabs.de/tokenops/internal/contexts/work/fromstory",
	"go.klarlabs.de/tokenops/internal/contexts/work",
	"go.klarlabs.de/tokenops/internal/contexts/telemetry/opencodedb",
	"go.klarlabs.de/tokenops/internal/contexts/telemetry/retention",
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow",
	"go.klarlabs.de/tokenops/internal/domainevents",
}

// depsMap memoises per-package transitive deps so the two arch tests
// share one subprocess invocation per domain package instead of
// duplicating work.
type depsMap map[string]map[string]struct{}

var (
	cachedDeps   depsMap
	cachedDepsMu sync.Mutex
)

func loadDepsMap(t *testing.T) depsMap {
	t.Helper()
	cachedDepsMu.Lock()
	defer cachedDepsMu.Unlock()
	if cachedDeps != nil {
		return cachedDeps
	}
	m := depsMap{}
	for _, pkg := range domainPackages {
		m[pkg] = transitiveDeps(t, pkg)
	}
	cachedDeps = m
	return m
}

func TestNoDomainImportsAdapter(t *testing.T) {
	deps := loadDepsMap(t)
	for pkg, d := range deps {
		for _, banned := range forbiddenAdapters {
			if _, ok := d[banned]; ok {
				t.Errorf("DDD layering violation: %s transitively imports %s\n"+
					"  see docs/architecture-ddd.md", pkg, banned)
			}
		}
	}
}

func TestNoDomainImportsInfraExceptDocumented(t *testing.T) {
	// Direct imports only: depending on replay/analytics (which talk to
	// sqlite behind a port) is allowed. Importing sqlite yourself is not,
	// unless the package is in storageExempt.
	for _, pkg := range domainPackages {
		if storageExempt[pkg] {
			continue
		}
		direct := directImports(t, pkg)
		for _, banned := range forbiddenInfra {
			if _, ok := direct[banned]; ok {
				t.Errorf("DDD layering violation: %s imports %s directly\n"+
					"  add an exemption (with rationale) to storageExempt + docs/architecture-ddd.md", pkg, banned)
			}
		}
	}
}

// isOuterLayer reports whether imp falls under forbiddenOuterPrefixes.
func isOuterLayer(imp string) bool {
	for _, p := range forbiddenOuterPrefixes {
		if strings.HasSuffix(p, "/") {
			if strings.HasPrefix(imp, p) {
				return true
			}
			continue
		}
		if imp == p || strings.HasPrefix(imp, p+"/") {
			return true
		}
	}
	return false
}

func TestNoDomainImportsOuterLayer(t *testing.T) {
	for _, pkg := range domainPackages {
		exempt := map[string]bool{}
		for _, imp := range outerImportExempt[pkg] {
			exempt[imp] = true
		}
		for imp := range directImports(t, pkg) {
			if isOuterLayer(imp) && !exempt[imp] {
				t.Errorf("DDD layering violation: %s imports %s directly\n"+
					"  domain packages must not depend on daemon, config, bootstrap, "+
					"presentation or internal/infra; declare a port in the domain and "+
					"let the adapter satisfy it (outerImportExempt only shrinks)", pkg, imp)
			}
		}
	}
}

func TestOuterImportExemptNotStale(t *testing.T) {
	listed := map[string]bool{}
	for _, pkg := range domainPackages {
		listed[pkg] = true
	}
	for pkg, imps := range outerImportExempt {
		if !listed[pkg] {
			t.Errorf("outerImportExempt lists %s but it is not in domainPackages", pkg)
			continue
		}
		direct := directImports(t, pkg)
		for _, imp := range imps {
			if !isOuterLayer(imp) {
				t.Errorf("outerImportExempt[%s] lists %s, which no rule forbids; delete it", pkg, imp)
			}
			if _, ok := direct[imp]; !ok {
				t.Errorf("outerImportExempt[%s] lists %s but the package no longer imports it — "+
					"delete the entry so the ratchet records the progress", pkg, imp)
			}
		}
	}
}

// TestNoDomainImportsIO keeps HTTP clients, subprocesses and SQL out of the
// domain tree: a domain package declares the port, an internal/infra
// package does the I/O.
func TestNoDomainImportsIO(t *testing.T) {
	for _, pkg := range domainPackages {
		exempt := map[string]bool{}
		for _, imp := range ioImportExempt[pkg] {
			exempt[imp] = true
		}
		direct := directImports(t, pkg)
		for _, banned := range forbiddenIOImports {
			if _, ok := direct[banned]; ok && !exempt[banned] {
				t.Errorf("DDD layering violation: %s imports %s\n"+
					"  domain packages do no I/O of their own: declare a port in the domain, "+
					"implement it under internal/infra and wire it in the composition root "+
					"(ioImportExempt only shrinks)", pkg, banned)
			}
		}
	}
}

func TestIOImportExemptNotStale(t *testing.T) {
	listed := map[string]bool{}
	for _, pkg := range domainPackages {
		listed[pkg] = true
	}
	banned := map[string]bool{}
	for _, imp := range forbiddenIOImports {
		banned[imp] = true
	}
	for pkg, imps := range ioImportExempt {
		if !listed[pkg] {
			t.Errorf("ioImportExempt lists %s but it is not in domainPackages", pkg)
			continue
		}
		direct := directImports(t, pkg)
		for _, imp := range imps {
			if !banned[imp] {
				t.Errorf("ioImportExempt[%s] lists %s, which no rule forbids; delete it", pkg, imp)
			}
			if _, ok := direct[imp]; !ok {
				t.Errorf("ioImportExempt[%s] lists %s but the package no longer imports it — "+
					"delete the entry so the ratchet records the progress", pkg, imp)
			}
		}
	}
}

// TestEventsPackageStaysAPort keeps internal/events eligible for the
// domain-facing exemption above: it may import the standard library and
// pkg/eventschema, and nothing else from this module.
func TestEventsPackageStaysAPort(t *testing.T) {
	const module = "go.klarlabs.de/tokenops/"
	for imp := range directImports(t, module+"internal/events") {
		if strings.HasPrefix(imp, module) && imp != module+"pkg/eventschema" {
			t.Errorf("internal/events imports %s; domains depend on it as a port, so it "+
				"must stay free of other module packages", imp)
		}
	}
}

func TestDomainPackagesComplete(t *testing.T) {
	cmd := exec.Command("go", "list", "go.klarlabs.de/tokenops/internal/contexts/...")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list contexts: %v", err)
	}
	listed := map[string]struct{}{}
	for _, pkg := range domainPackages {
		listed[pkg] = struct{}{}
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		pkg := strings.TrimSpace(line)
		if pkg == "" {
			continue
		}
		if _, ok := listed[pkg]; !ok {
			t.Errorf("archlint domainPackages missing %s\n  add it (CI contract in AGENTS.md)", pkg)
		}
		delete(listed, pkg)
	}
	for pkg := range listed {
		if pkg == "go.klarlabs.de/tokenops/internal/domainevents" {
			continue
		}
		t.Errorf("archlint domainPackages has stale %s (not under internal/contexts/...)", pkg)
	}
}

func TestStorageExemptImportsSQLite(t *testing.T) {
	sqlitePkg := "go.klarlabs.de/tokenops/internal/storage/sqlite"
	listed := map[string]struct{}{}
	for _, pkg := range domainPackages {
		listed[pkg] = struct{}{}
	}
	for pkg, exempt := range storageExempt {
		if !exempt {
			t.Errorf("storageExempt[%s]=false is dead noise; delete the entry", pkg)
			continue
		}
		if _, ok := listed[pkg]; !ok {
			t.Errorf("storageExempt lists %s but it is not in domainPackages", pkg)
			continue
		}
		if _, has := directImports(t, pkg)[sqlitePkg]; !has {
			t.Errorf("storageExempt lists %s but it does not directly import %s", pkg, sqlitePkg)
		}
	}
}

func directImports(t *testing.T, pkg string) map[string]struct{} {
	t.Helper()
	cmd := exec.Command("go", "list", "-f", "{{range .Imports}}{{println .}}{{end}}", pkg)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list imports %s: %v", pkg, err)
	}
	set := map[string]struct{}{}
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		set[line] = struct{}{}
	}
	return set
}

func transitiveDeps(t *testing.T, pkg string) map[string]struct{} {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	set := map[string]struct{}{}
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		set[line] = struct{}{}
	}
	return set
}
