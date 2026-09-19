package mcp

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func enterpriseDeps(t *testing.T, events ...*eventschema.Envelope) PlanDeps {
	t.Helper()
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "e.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if len(events) > 0 {
		if err := st.AppendBatch(context.Background(), events); err != nil {
			t.Fatal(err)
		}
	}
	return PlanDeps{
		Config: &config.Config{
			Plans:      map[string]string{"anthropic": "claude-enterprise"},
			PlanLimits: map[string]config.PlanLimit{"anthropic": {SpendLimitUSD: 5000}},
		},
		Store: st,
	}
}

// tokenops_plan_headroom never read the Enterprise spend limit: an agent was
// told no limit was configured when the terminal showed one.
func TestPlanHeadroomToolReadsTheConfiguredSpendLimit(t *testing.T) {
	res, err := planHeadroom(context.Background(), enterpriseDeps(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Reports) != 1 || res.Reports[0].SpendLimitUSD != 5000 {
		t.Fatalf("reports = %+v, want the configured 5000 limit", res.Reports)
	}
}

// With a Claude usage meter reading, the agent gets Anthropic's own spend
// and limit.
func TestPlanHeadroomToolPrefersTheVendorSpend(t *testing.T) {
	reading := &eventschema.Envelope{
		ID:            "ack-test",
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
	res, err := planHeadroom(context.Background(), enterpriseDeps(t, reading))
	if err != nil {
		t.Fatal(err)
	}
	r := res.Reports[0]
	if r.SpendSource != "vendor" || r.SpendUSD != 1095.63 || r.SpendLimitUSD != 1500 {
		t.Errorf("report = %s %.2f of %.2f, want vendor 1095.63 of 1500", r.SpendSource, r.SpendUSD, r.SpendLimitUSD)
	}
}
