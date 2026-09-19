package mcp

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// allRegisteredTools builds a server with every tool group registered, the
// way serve does, and returns what a client would see in tools/list.
// Deps are the minimum that make conditionally-registered tools appear.
func allRegisteredTools(t *testing.T) map[string]bool {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "h.db"), sqlite.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv := NewServer("tokenops", "test", nil)
	eng := spend.NewEngine(spend.DefaultTable())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	must(RegisterTools(srv, Deps{Store: store, Spend: eng, Aggregator: analytics.New(store, eng)}))
	must(RegisterRulesTools(srv))
	must(RegisterParityTools(srv, ParityDeps{Store: store, Spend: eng}))
	must(RegisterControlTools(srv, ControlDeps{}))
	must(RegisterPlanTools(srv, PlanDeps{}))
	must(RegisterAgentDXTools(srv, AgentDXDeps{}))
	must(RegisterStoryTools(srv, StoryDeps{}))
	must(RegisterRoutingAdviceTools(srv, RoutingAdviceDeps{}))
	must(RegisterApprovalTools(srv, ApprovalDeps{}))
	must(RegisterModeTools(srv, ModeDeps{}))
	must(RegisterHelpTool(srv))
	must(RegisterDataSourcesTool(srv, DataSourcesDeps{Store: store}))
	must(RegisterDashboardTool(srv))
	must(RegisterFmtTools(srv))
	must(RegisterCoachTools(srv, CoachDeps{}))
	must(RegisterGapTools(srv, GapDeps{}))
	must(RegisterSetupTools(srv, SetupDeps{}))

	out := map[string]bool{}
	for _, ti := range srv.Tools() {
		out[ti.Name] = true
	}
	return out
}

// tokenops_help listed 23 of 41 tools. The catalog's own comment said a
// new tool "requires adding it here" and named arch tests as the safety
// net; there were none, so eighteen tools were added and never listed.
// This is that net, in both directions.
func TestHelpCatalogCoversEveryRegisteredTool(t *testing.T) {
	registered := allRegisteredTools(t)
	cataloged := map[string]bool{}
	for _, c := range helpCatalog {
		for _, tool := range c.Tools {
			if cataloged[tool.Name] {
				t.Errorf("%s is listed twice in helpCatalog", tool.Name)
			}
			cataloged[tool.Name] = true
			if strings.TrimSpace(tool.Summary) == "" {
				t.Errorf("%s has no summary", tool.Name)
			}
		}
	}
	var missing, phantom []string
	for name := range registered {
		if !cataloged[name] {
			missing = append(missing, name)
		}
	}
	for name := range cataloged {
		if !registered[name] {
			phantom = append(phantom, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(phantom)
	for _, name := range missing {
		t.Errorf("tool %s is registered but absent from helpCatalog — add it to a category", name)
	}
	for _, name := range phantom {
		t.Errorf("helpCatalog lists %s, which no Register* function registers", name)
	}
}

var helpRegisterRe = regexp.MustCompile(`(?m)^func (Register[A-Za-z]+)\(`)

// allRegisteredTools builds its own server, so a tool group this file
// forgets to register is invisible to the check above. Every Register*
// function the package defines has to be called there.
func TestHelpTestRegistersEveryToolGroup(t *testing.T) {
	self, err := os.ReadFile("help_tool_test.go")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range helpRegisterRe.FindAllStringSubmatch(string(src), -1) {
			if !strings.Contains(string(self), "must("+m[1]+"(") {
				t.Errorf("%s (%s) is not registered in allRegisteredTools", m[1], e.Name())
			}
		}
	}
}

// The description hardcoded "20+" while the surface grew past forty.
func TestHelpDescriptionDoesNotHardcodeACount(t *testing.T) {
	srv := NewServer("tokenops", "test", nil)
	if err := RegisterHelpTool(srv); err != nil {
		t.Fatal(err)
	}
	for _, ti := range srv.Tools() {
		if regexp.MustCompile(`\d+\+`).MatchString(ti.Description) {
			t.Errorf("description hardcodes a tool count: %q", ti.Description)
		}
	}
}
