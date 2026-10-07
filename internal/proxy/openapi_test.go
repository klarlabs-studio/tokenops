package proxy

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

var update = flag.Bool("update", false, "rewrite docs/api/openapi.json")

const openAPIPath = "../../docs/api/openapi.json"

// fullyWired is the daemon's API with every option it sets.
func fullyWired() *Server {
	return New("127.0.0.1:0", WithDashAuth(allowAll{}),
		WithAnalytics(&AnalyticsHandlers{}), WithRules(&RulesHandlers{}), WithAudit(&AuditHandlers{}),
		WithEventCounts(func() map[string]int64 { return nil }),
		WithSourceFreshness(func() []freshness.Report { return nil }),
		WithSourcesRefresh(func(time.Time) (int, bool, time.Time) { return 0, true, time.Time{} }),
		WithPlans(func() headroom.Deps { return headroom.Deps{} }),
		WithState(func() state.Deps { return state.Deps{} }),
		WithSessions(func() SessionRoots { return SessionRoots{} }),
		WithActions(func() ActionDeps { return ActionDeps{} }))
}

// The catalog the document is made from lists exactly the routes the
// daemon registers: a route added without documenting it, or documented
// and since removed, fails here.
func TestAPICatalogMatchesTheRoutes(t *testing.T) {
	got, want := strings.Join(fullyWired().APIRoutes(), "\n"), strings.Join(sortedPatterns(), "\n")
	if got != want {
		t.Fatalf("registered routes:\n%s\n\ncatalog:\n%s", got, want)
	}
}

// docs/api/openapi.json is generated, never edited. Regenerate with
// go test ./internal/proxy -run TestOpenAPIDocumentIsCurrent -update
func TestOpenAPIDocumentIsCurrent(t *testing.T) {
	doc, err := OpenAPI("local")
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if strings.Contains(string(doc), "#/$defs/") {
		t.Error("a reference points into a schema's own $defs, which does not resolve in OpenAPI")
	}
	if *update {
		if err := os.WriteFile(filepath.Clean(openAPIPath), doc, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	current, err := os.ReadFile(filepath.Clean(openAPIPath))
	if err != nil {
		t.Fatalf("%v — generate it with -update", err)
	}
	if string(current) != string(doc) {
		t.Fatal("docs/api/openapi.json is stale; regenerate with: go test ./internal/proxy -run TestOpenAPIDocumentIsCurrent -update")
	}
}

// The docs page names every route, so a route cannot ship undocumented.
func TestAPIDocsPageListsEveryRoute(t *testing.T) {
	page, err := os.ReadFile(filepath.Clean("../../web/docs/guide/api.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range APICatalog {
		if !strings.Contains(string(page), "`"+r.Method+" "+r.Path+"`") {
			t.Errorf("web/docs/guide/api.md does not list %s", r.Pattern())
		}
	}
}
