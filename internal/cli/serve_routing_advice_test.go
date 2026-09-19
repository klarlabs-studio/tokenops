package cli

import (
	"os"
	"regexp"
	"testing"
)

// tokenops_routing_advise says it picks "what the pricing table currently
// calls cheapest", but it built its own engine over the compiled-in table
// while every other tool in serve priced with the daemon-refreshed card.
// A nil Spend still works — it falls back to the compiled-in table — so
// forgetting the wiring fails silently; this is the check that does not.
func TestServeWiresTheSpendEngineIntoRoutingAdvice(t *testing.T) {
	src, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatalf("read serve.go: %v", err)
	}
	wired := regexp.MustCompile(`RoutingAdviceDeps\{[^}]*\bSpend:\s*components\.Spend\b`)
	if !wired.Match(src) {
		t.Error("serve.go registers routing advice without Spend: components.Spend — it would price with the compiled-in table")
	}
}
