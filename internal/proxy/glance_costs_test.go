package proxy

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// usageOf answers every window with the same totals.
type usageOf struct{ tokens int64 }

func (u usageOf) Summarize(context.Context, analytics.Filter) (analytics.Summary, error) {
	return analytics.Summary{TotalTokens: u.tokens, Requests: 1}, nil
}

// The glance the daemon answers carries each plan's provider's usage, so
// a client draws the cards without reading 30 days of usage itself.
func TestGlanceCarriesItsProvidersCosts(t *testing.T) {
	now := time.Now().UTC()
	reading := &eventschema.Envelope{Source: "claude-code-statusline", Timestamp: now.Add(-time.Minute),
		Type: eventschema.EventTypePrompt, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
		Attributes: map[string]string{
			"five_hour_used_pct": "72.00", "five_hour_reset_at": now.Add(time.Hour).Format(time.RFC3339),
			"granularity": "quota_snapshot",
		}}
	cfg := &config.Config{Plans: map[string]string{"anthropic": "claude-ultra"}}
	deps := func() headroom.Deps {
		return headroom.Deps{Config: cfg, Reader: planEvents{[]*eventschema.Envelope{reading}}}
	}
	for _, withCosts := range []bool{true, false} {
		opts := []Option{WithPlans(deps)}
		if withCosts {
			opts = append(opts, WithGlanceCosts(usageOf{tokens: 1234}))
		}
		s := New("127.0.0.1:0", opts...)
		var g headroom.GlancePayload
		if code := getAPI(t, s, "/api/glance", &g); code != http.StatusOK {
			t.Fatalf("GET /api/glance = %d", code)
		}
		c, ok := g.ProviderCosts["anthropic"]
		if withCosts && (!ok || c.Today.Tokens != 1234 || c.Last30.Tokens != 1234 || g.ProviderCostsLate) {
			t.Errorf("costs = %+v (late %v), want anthropic's usage read through the summarizer", g.ProviderCosts, g.ProviderCostsLate)
		}
		if !withCosts && g.ProviderCosts != nil {
			t.Errorf("costs = %+v without a summarizer, want none", g.ProviderCosts)
		}
	}
}

// The CLI lists every finding, and says which the menu bar hides: asked
// for them, the route includes the dismissed ones.
func TestFindingsIncludeTheDismissedOnRequest(t *testing.T) {
	s, _ := newFindingsServer(t)
	var all findings.Report
	if code := getAPI(t, s, "/api/findings", &all); code != http.StatusOK || len(all.Findings) < 2 {
		t.Fatalf("GET /api/findings = %d with %s; want at least two findings", code, findingKeys(all))
	}
	if rec := post(s, "/api/findings/dismiss", "application/json", `{"key":"boundary:headroom-pause","for":"week"}`); rec.Code != http.StatusOK {
		t.Fatalf("dismiss = %d %s", rec.Code, rec.Body)
	}
	var shownNow, included findings.Report
	getAPI(t, s, "/api/findings", &shownNow)
	if _, ok := shownNow.Find("boundary:headroom-pause"); ok {
		t.Errorf("the dismissed finding is shown: %s", findingKeys(shownNow))
	}
	if code := getAPI(t, s, "/api/findings?dismissed=include", &included); code != http.StatusOK {
		t.Fatalf("GET /api/findings?dismissed=include = %d", code)
	}
	if findingKeys(included) != findingKeys(all) {
		t.Errorf("with the dismissed: %s, want every finding %s", findingKeys(included), findingKeys(all))
	}
	var bad struct{ Error string }
	if code := getAPI(t, s, "/api/findings?dismissed=yes", &bad); code != http.StatusBadRequest {
		t.Errorf("GET /api/findings?dismissed=yes = %d, want 400", code)
	}
}
