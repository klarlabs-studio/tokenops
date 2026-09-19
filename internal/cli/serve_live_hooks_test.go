package cli

import (
	"os"
	"regexp"
	"testing"
)

// These hooks all have a working fallback — the value serve started with —
// so forgetting to wire one fails silently: the tool keeps answering from
// the startup config until the MCP client restarts the server. This is the
// check that does not.
func TestServeWiresTheLiveConfigHooks(t *testing.T) {
	src, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatalf("read serve.go: %v", err)
	}
	for name, re := range map[string]*regexp.Regexp{
		"waste config":     regexp.MustCompile(`mcp\.Deps\{[\s\S]*?\bWasteConfig:\s*func\(\)`),
		"replay pipeline":  regexp.MustCompile(`ParityDeps\{[\s\S]*?\bPipelineFor:\s*func\(\)`),
		"coach root":       regexp.MustCompile(`CoachDeps\{[\s\S]*?\bRootFor:\s*func\(\)`),
		"session provider": regexp.MustCompile(`SessionMiddleware\(tracker,\s*liveProvider\)`),
		"dashboard token":  regexp.MustCompile(`DashboardDeps\{[\s\S]*?\bToken:\s*func\(\)`),
	} {
		if !re.Match(src) {
			t.Errorf("serve.go does not wire the live %s hook", name)
		}
	}
}
