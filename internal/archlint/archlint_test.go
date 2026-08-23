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
// abstraction so packages that depend on analytics.Row (forecast,
// spend) are still allowed; they must NOT import sqlite themselves.
var forbiddenInfra = []string{
	"go.klarlabs.de/tokenops/internal/storage/sqlite",
}

// storageExempt domains legitimately import sqlite via an isolated
// adapter file. Only packages that actually import
// internal/storage/sqlite belong here — TestStorageExemptImportsSQLite
// fails the build if an exemption is stale. Documented in
// docs/architecture-ddd.md.
var storageExempt = map[string]bool{
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard":    true,
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics": true,
	"go.klarlabs.de/tokenops/internal/contexts/security/audit":          true,
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow":      true,
	"go.klarlabs.de/tokenops/internal/contexts/optimization/replay":     true,
	"go.klarlabs.de/tokenops/internal/contexts/telemetry/retention":     true,
	"go.klarlabs.de/tokenops/internal/contexts/tasks":                   true,
}

// domainPackages lists every domain package the arch test enforces.
// Every package under internal/contexts/* belongs here so new contexts
// are gated automatically — TestDomainPackagesComplete compares this
// list to `go list ./internal/contexts/...`.
var domainPackages = []string{
	"go.klarlabs.de/tokenops/internal/contexts/coaching/coaching",
	"go.klarlabs.de/tokenops/internal/contexts/coaching/efficiency",
	"go.klarlabs.de/tokenops/internal/contexts/coaching/prompts",
	"go.klarlabs.de/tokenops/internal/contexts/coaching/replies",
	"go.klarlabs.de/tokenops/internal/contexts/coaching/tools",
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste",
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget",
	"go.klarlabs.de/tokenops/internal/contexts/governance/coverdebt",
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard",
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics",
	"go.klarlabs.de/tokenops/internal/contexts/observability/anomaly",
	"go.klarlabs.de/tokenops/internal/contexts/observability/observ",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/eval",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/fmtlearn",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/contexttrim",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/dedupe",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/promptcompress",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/qualitygate",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/retrievalprune",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/toolfmt",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/replay",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass",
	"go.klarlabs.de/tokenops/internal/contexts/prompts/llm",
	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers",
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer",
	"go.klarlabs.de/tokenops/internal/contexts/rules",
	"go.klarlabs.de/tokenops/internal/contexts/security/audit",
	"go.klarlabs.de/tokenops/internal/contexts/security/dashauth",
	"go.klarlabs.de/tokenops/internal/contexts/security/rbac",
	"go.klarlabs.de/tokenops/internal/contexts/security/redaction",
	"go.klarlabs.de/tokenops/internal/contexts/security/tlsmint",
	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast",
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans",
	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing",
	"go.klarlabs.de/tokenops/internal/contexts/spend/session",
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropiccookie",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecode",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/copilot",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursor",
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/opencode",
	"go.klarlabs.de/tokenops/internal/contexts/tasks",
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
