package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type planEvents struct{ events []*eventschema.Envelope }

func (p planEvents) ReadEvents(context.Context, eventschema.EventType, time.Time) ([]*eventschema.Envelope, error) {
	return p.events, nil
}

func (p planEvents) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return map[string]int64{}, nil
}

func getAPI(t *testing.T, s *Server, path string, into any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	s.apiMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("GET %s: %v: %s", path, err, rec.Body.String())
	}
	return rec.Code
}

// No plan bound is an answer, in the same {error, hint} shape the MCP
// tools give, not a failure.
func TestPlanRoutesAnswerUnconfigured(t *testing.T) {
	s := New("127.0.0.1:0", WithPlans(func() headroom.Deps { return headroom.Deps{} }))
	for _, path := range []string{"/api/plans/headroom", "/api/plans/session-budget"} {
		var body struct{ Error, Hint string }
		if code := getAPI(t, s, path, &body); code != http.StatusOK || body.Error != headroom.ErrPlansUnconfigured || body.Hint == "" {
			t.Errorf("GET %s = %d %+v", path, code, body)
		}
	}
	var glance headroom.GlancePayload
	if code := getAPI(t, s, "/api/glance", &glance); code != http.StatusOK || glance.Insight.Level != "unavailable" {
		t.Errorf("GET /api/glance = %d %+v", code, glance.Insight)
	}
}

// The routes serve the vendor's windows the capability computes.
func TestPlanRoutesServeTheVendorWindows(t *testing.T) {
	now := time.Now().UTC()
	reading := &eventschema.Envelope{Source: "claude-code-statusline", Timestamp: now.Add(-time.Minute),
		Type: eventschema.EventTypePrompt, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
		Attributes: map[string]string{
			"five_hour_used_pct": "72.00", "five_hour_reset_at": now.Add(time.Hour).Format(time.RFC3339),
			"granularity": "quota_snapshot",
		}}
	cfg := &config.Config{Plans: map[string]string{"anthropic": "claude-max-20x"}}
	s := New("127.0.0.1:0", WithPlans(func() headroom.Deps {
		return headroom.Deps{Config: cfg, Reader: planEvents{[]*eventschema.Envelope{reading}}}
	}))

	var head headroom.HeadroomPayload
	if code := getAPI(t, s, "/api/plans/headroom", &head); code != http.StatusOK || len(head.Reports) != 1 {
		t.Fatalf("headroom = %d %+v", code, head)
	}
	if w := head.Reports[0].Windows; len(w) != 1 || w[0].UsedPct != 72 {
		t.Errorf("windows = %+v", w)
	}
	var budget headroom.BudgetPayload
	if code := getAPI(t, s, "/api/plans/session-budget", &budget); code != http.StatusOK || len(budget.Budgets) != 1 {
		t.Fatalf("session budget = %d %+v", code, budget)
	}
	if !s.ServesAPI(http.MethodGet, "/api/glance") || s.ServesAPI(http.MethodGet, "/api/nothing") {
		t.Error("ServesAPI does not reflect the routes")
	}
}
