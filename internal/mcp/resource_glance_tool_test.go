package mcp

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestResourceGlanceUsesSpendHeadroomWhenPlanHasNoSessionWindow(t *testing.T) {
	reading := &eventschema.Envelope{
		ID:            "resource-glance-usage",
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Timestamp:     time.Now().UTC().Add(-time.Minute),
		Source:        "claude-usage-meter",
		Attributes: map[string]string{
			"extra_usage_used": "1095.63", "extra_usage_limit": "1500.00",
			"extra_usage_currency": "USD", "extra_usage_limit_reached": "false",
		},
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, Status: 200},
	}
	d := enterpriseDeps(t, reading)
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := RegisterPlanTools(srv, d); err != nil {
		t.Fatalf("RegisterPlanTools: %v", err)
	}
	out := execTool(t, srv, "tokenops_resource_glance", nil)
	for _, want := range []string{`"level": "attention"`, `"basis": "plan_headroom"`, `"overage_risk": "medium"`, `"provider": "anthropic"`} {
		if !strings.Contains(out, want) {
			t.Errorf("resource glance missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "no rate-limit window") {
		t.Errorf("resource glance dropped the windowless-plan explanation:\n%s", out)
	}
}

func TestResourceGlanceDoesNotCallUnconfiguredCapacityClear(t *testing.T) {
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := RegisterPlanTools(srv, PlanDeps{}); err != nil {
		t.Fatalf("RegisterPlanTools: %v", err)
	}
	out := execTool(t, srv, "tokenops_resource_glance", nil)
	if !strings.Contains(out, `"level": "unavailable"`) {
		t.Fatalf("expected unavailable, not clear, without configured measurements:\n%s", out)
	}
	if strings.Contains(out, `"level": "clear"`) {
		t.Fatalf("missing measurements were presented as clear:\n%s", out)
	}
}

func TestResourceInsightPrioritizesSessionIntervention(t *testing.T) {
	insight := resourceInsight(&sessionBudgetResult{Budgets: []plans.SessionBudget{{
		Provider: "anthropic", Display: "Claude Max", RecommendedAction: plans.ActionWaitReset,
		WindowPct: 96, Confidence: plans.ConfidenceHigh,
		SignalQuality: plans.SignalQuality{Level: plans.SignalLevelLow, Caveat: "MCP pings only"},
	}}}, &planHeadroomResult{Reports: []plans.HeadroomReport{{Provider: "anthropic", Display: "Claude Max", OverageRisk: plans.RiskLow}}})
	if insight.Level != "attention" || insight.RecommendedAction != plans.ActionWaitReset {
		t.Fatalf("insight = %+v, want wait_for_reset attention", insight)
	}
	if insight.SignalQualityLevel != plans.SignalLevelLow || insight.Caveat != "MCP pings only" {
		t.Fatalf("insight dropped the signal caveat: %+v", insight)
	}
}

func TestResourceInsightDoesNotCallDemoDominatedReadingsClear(t *testing.T) {
	insight := resourceInsight(&sessionBudgetResult{
		Budgets:     []plans.SessionBudget{{Provider: "anthropic", Display: "Claude Max", RecommendedAction: plans.ActionContinue, SignalQuality: plans.SignalQuality{Level: plans.SignalLevelHigh}}},
		DataWarning: &DataWarning{SyntheticRatioPct: 90},
	}, &planHeadroomResult{Reports: []plans.HeadroomReport{{Provider: "anthropic", Display: "Claude Max", OverageRisk: plans.RiskLow}}})
	if insight.Level != "uncertain" || !strings.Contains(insight.Caveat, "90.0%") {
		t.Fatalf("demo-dominated insight = %+v, want uncertain with the synthetic ratio", insight)
	}
}
