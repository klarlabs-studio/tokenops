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
// adapter file. The exemption is documented in
// docs/architecture-ddd.md.
var storageExempt = map[string]bool{
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard":    true,
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget":       true,
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics": true,
	"go.klarlabs.de/tokenops/internal/contexts/security/audit":          true,
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow":      true,
	"go.klarlabs.de/tokenops/internal/contexts/observability/anomaly":   true,
	"go.klarlabs.de/tokenops/internal/contexts/optimization/replay":     true,
	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast":          true,
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend":             true,
	"go.klarlabs.de/tokenops/internal/contexts/telemetry/retention":     true,
	"go.klarlabs.de/tokenops/internal/contexts/optimization/eval":       true,
	"go.klarlabs.de/tokenops/internal/contexts/coaching/coaching":       true,
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste":          true,
	"go.klarlabs.de/tokenops/internal/contexts/coaching/efficiency":     true,
	"go.klarlabs.de/tokenops/internal/contexts/security/dashauth":       true,
	"go.klarlabs.de/tokenops/internal/contexts/security/rbac":           true,
	"go.klarlabs.de/tokenops/internal/domainevents":                     false,
}

// domainPackages lists every domain package the arch test enforces.
// Every package under internal/contexts/* belongs here so new contexts
// are gated automatically.
var domainPackages = []string{
	"go.klarlabs.de/tokenops/internal/contexts/rules",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/eval",
	// Note: internal/infra/rulesfs is an infrastructure adapter; it
	// legitimately uses io/fs + os, so it is excluded from the domain
	// arch-lint sweep below.
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/formatter",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/fmtlearn",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/toolfmt",
	"go.klarlabs.de/tokenops/internal/contexts/optimization/replay",
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard",
	"go.klarlabs.de/tokenops/internal/contexts/governance/coverdebt",
	"go.klarlabs.de/tokenops/internal/contexts/governance/budget",
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow",
	"go.klarlabs.de/tokenops/internal/contexts/coaching/coaching",
	"go.klarlabs.de/tokenops/internal/contexts/coaching/efficiency",
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste",
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend",
	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast",
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics",
	"go.klarlabs.de/tokenops/internal/contexts/observability/anomaly",
	"go.klarlabs.de/tokenops/internal/contexts/observability/observ",
	"go.klarlabs.de/tokenops/internal/contexts/security/redaction",
	"go.klarlabs.de/tokenops/internal/contexts/security/audit",
	"go.klarlabs.de/tokenops/internal/contexts/security/dashauth",
	"go.klarlabs.de/tokenops/internal/contexts/security/rbac",
	"go.klarlabs.de/tokenops/internal/contexts/security/tlsmint",
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer",
	"go.klarlabs.de/tokenops/internal/contexts/prompts/providers",
	"go.klarlabs.de/tokenops/internal/contexts/prompts/llm",
	"go.klarlabs.de/tokenops/internal/contexts/telemetry/retention",
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
	deps := loadDepsMap(t)
	for pkg, d := range deps {
		if storageExempt[pkg] {
			continue
		}
		for _, banned := range forbiddenInfra {
			if _, ok := d[banned]; ok {
				t.Errorf("DDD layering violation: %s transitively imports %s\n"+
					"  add an exemption (with rationale) to storageExempt + docs/architecture-ddd.md", pkg, banned)
			}
		}
	}
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
