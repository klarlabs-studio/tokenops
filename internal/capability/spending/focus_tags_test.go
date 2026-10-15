package spending

import (
	"context"
	"math"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
)

// The part of a charge an allocation rule tagged becomes its own row with
// its tags; the rest stays untagged; the rows still add up to the charge.
func TestFocusExportSplitsChargesByAllocationTag(t *testing.T) {
	sep3 := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	src := &chargesStub{charges: []analytics.Charge{
		{Day: sep3, Provider: "openai", Model: "gpt-5", Requests: 3, InputTokens: 300, OutputTokens: 30,
			CostUSD: 0.9, ListUSD: 0.9},
		{Day: sep3, Provider: "anthropic", Model: "claude-opus-5", PlanCovered: true, Requests: 2,
			InputTokens: 2000, OutputTokens: 200, ListUSD: 4},
	}}
	at := sep3.Add(10 * time.Hour)
	turns := []analytics.SessionTurn{
		{SessionID: "acme", Source: "codex-jsonl", At: at, Provider: "openai", Model: "gpt-5",
			InputTokens: 200, OutputTokens: 20, CostUSD: 0.6, APIEquivalentUSD: 0.6, Priced: true},
		{SessionID: "acme", Source: "claude-code-jsonl", At: at, Provider: "anthropic", Model: "claude-opus-5", PlanCovered: true,
			InputTokens: 1000, OutputTokens: 100, APIEquivalentUSD: 2, Priced: true},
		{SessionID: "other", Source: "claude-code-jsonl", At: at, Provider: "anthropic", Model: "claude-opus-5", PlanCovered: true,
			InputTokens: 1000, OutputTokens: 100, APIEquivalentUSD: 2, Priced: true},
	}
	rules := []allocation.Rule{{Match: allocation.Match{Dir: "/w/acme"}, Tags: allocation.Tags{Client: "Acme Corp"}}}
	got, err := FocusExport(context.Background(), src, FocusQuery{
		Since: sep3, Until: sep3.Add(24 * time.Hour), Now: sep3.Add(24 * time.Hour),
		Allocation: &allocation.Deps{
			Turns: func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return turns, nil },
			Dirs: func(time.Time) map[string]sessiondirs.Dir {
				return map[string]sessiondirs.Dir{"acme": {CWD: "/w/acme/app"}, "other": {CWD: "/w/else"}}
			},
			Rules: rules,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	type cell struct {
		billed, list  float64
		consumedTotal int64
	}
	byTag := map[string]map[string]cell{}
	for _, r := range got.Rows {
		if byTag[r.SkuID] == nil {
			byTag[r.SkuID] = map[string]cell{}
		}
		c := byTag[r.SkuID][r.Tags]
		c.billed += r.BilledCost
		c.list += r.ListCost
		c.consumedTotal += *r.ConsumedQuantity
		byTag[r.SkuID][r.Tags] = c
	}
	const acme = `{"client":"Acme Corp"}`
	gpt := byTag["gpt-5"]
	if len(gpt) != 2 || math.Abs(gpt[acme].billed-0.6) > 1e-9 || gpt[acme].consumedTotal != 220 ||
		math.Abs(gpt[""].billed-0.3) > 1e-9 || gpt[""].consumedTotal != 110 {
		t.Errorf("gpt-5 rows = %+v", gpt)
	}
	opus := byTag["claude-opus-5"]
	if len(opus) != 2 || opus[acme].billed != 0 || opus[acme].list != 2 || opus[""].list != 2 || opus[""].consumedTotal != 1100 {
		t.Errorf("claude-opus-5 rows = %+v", opus)
	}
	for sku, cells := range byTag {
		var total cell
		for _, c := range cells {
			total.billed += c.billed
			total.list += c.list
		}
		want := map[string]float64{"gpt-5": 0.9, "claude-opus-5": 0}[sku]
		if math.Abs(total.billed-want) > 1e-9 {
			t.Errorf("%s rows bill %v, the charge %v", sku, total.billed, want)
		}
	}
}

// When every turn of a charge is tagged, no empty untagged row is left,
// and the tagged rows carry the charge whole.
func TestFocusExportLeavesNoEmptyRemainder(t *testing.T) {
	sep3 := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	src := &chargesStub{charges: []analytics.Charge{
		{Day: sep3, Provider: "openai", Model: "gpt-5", Requests: 1, InputTokens: 100, OutputTokens: 10, CostUSD: 0.31, ListUSD: 0.31},
	}}
	turns := []analytics.SessionTurn{{SessionID: "s", Source: "codex-jsonl", At: sep3.Add(time.Hour), Provider: "openai",
		Model: "gpt-5", InputTokens: 100, OutputTokens: 10, APIEquivalentUSD: 0.3, Priced: true}}
	got, err := FocusExport(context.Background(), src, FocusQuery{
		Since: sep3, Until: sep3.Add(24 * time.Hour), Now: sep3.Add(24 * time.Hour),
		Allocation: &allocation.Deps{
			Turns: func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return turns, nil },
			Dirs:  func(time.Time) map[string]sessiondirs.Dir { return nil },
			Rules: []allocation.Rule{{Match: allocation.Match{Harness: "codex"}, Tags: allocation.Tags{CostCenter: "cc-1"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0].Tags != `{"cost_center":"cc-1"}` || got.Rows[0].BilledCost != 0.31 {
		t.Errorf("rows = %+v", got.Rows)
	}
}
