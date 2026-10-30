package focus_test

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/focus"
)

// The team plane's organisation dataset names no model (ADR 0012 §11):
// each row is a provider's tokens on a day, priced at the blend of the
// models used. It is FOCUS 1.4 like the client's own export.
func TestBlendedRowsConformToFOCUS14(t *testing.T) {
	day := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	rows := []focus.Row{
		focus.Blended(focus.Blend{Day: day, Provider: "anthropic", Requests: 120, Tokens: 3_456_789,
			ListUSD: 12.345678, EstimatedUSD: 2.5, Tags: map[string]string{"x_repository": "acme/api"}}),
		focus.Blended(focus.Blend{Day: day, Provider: "openai", Requests: 1, Tokens: 7, ListUSD: 0.0001, Unpriced: true}),
		focus.Blended(focus.Blend{Day: day, Provider: "fireworks", Requests: 3, Tokens: 0}),
	}
	var buf bytes.Buffer
	if err := focus.V1_4.WriteCSV(&buf, rows); err != nil {
		t.Fatal(err)
	}
	for _, v := range validate14(t, buf.Bytes()) {
		t.Error(v)
	}
	r := rows[0]
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-6 }
	if r.BilledCost != 0 || r.EffectiveCost != 0 || r.EstimatedCost != 2.5 || !near(r.ContractedCost, 2.5) {
		t.Errorf("costs = billed %v effective %v estimated %v contracted %v", r.BilledCost, r.EffectiveCost, r.EstimatedCost, r.ContractedCost)
	}
	if !near(r.ListCost, 12.345678) {
		t.Errorf("ListCost = %v, want the list value", r.ListCost)
	}
	if r.SkuID != "anthropic/models" || r.SkuPriceID != "anthropic/models/tokens" || r.ProviderName != "Anthropic" {
		t.Errorf("sku %q / %q, provider %q", r.SkuID, r.SkuPriceID, r.ProviderName)
	}
	if !strings.Contains(r.ChargeDescription, "blend") || !strings.Contains(rows[1].ChargeDescription, "understates") {
		t.Errorf("descriptions do not say what the prices are: %q / %q", r.ChargeDescription, rows[1].ChargeDescription)
	}
	if !strings.Contains(r.Tags, `"x_repository":"acme/api"`) {
		t.Errorf("tags = %s", r.Tags)
	}
}
