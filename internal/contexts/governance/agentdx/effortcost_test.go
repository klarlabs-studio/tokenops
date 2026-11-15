package agentdx

import (
	"math"
	"testing"
	"time"
)

func TestByEffortCostedSumsAndSharesEachRow(t *testing.T) {
	// Two sessions at high effort, one at low. Costs are per unit; each
	// unit's cost is the turns of its session that fell inside it.
	base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	records := []Record{
		{At: base, SessionID: "a", Kind: KindPrompt},
		{At: base.Add(time.Minute), SessionID: "a", Kind: KindAssistantTurn, Model: "m", Effort: "high", InputTokens: 10},
		{At: base, SessionID: "b", Kind: KindPrompt},
		{At: base.Add(time.Minute), SessionID: "b", Kind: KindAssistantTurn, Model: "m", Effort: "high", InputTokens: 10},
		{At: base, SessionID: "c", Kind: KindPrompt},
		{At: base.Add(time.Minute), SessionID: "c", Kind: KindAssistantTurn, Model: "m", Effort: "low", InputTokens: 10},
	}
	cost := func(u Unit) float64 {
		return map[string]float64{"a": 3, "b": 1, "c": 1}[u.SessionID]
	}
	rows := ByEffortCosted(records, cost)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	var high, low EffortRow
	for _, r := range rows {
		if r.Effort == "high" {
			high = r
		} else {
			low = r
		}
	}
	cases := []struct {
		name       string
		row        EffortRow
		usd, share float64
		perInstr   float64
	}{
		{"high effort carries four of five dollars", high, 4, 80, 2},
		{"low effort carries one", low, 1, 20, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if math.Abs(tc.row.APIEquivalentUSD-tc.usd) > 1e-9 || math.Abs(tc.row.SharePct-tc.share) > 1e-9 ||
				math.Abs(tc.row.USDPerInstruction-tc.perInstr) > 1e-9 {
				t.Errorf("usd %v share %v per %v; want %v %v %v", tc.row.APIEquivalentUSD, tc.row.SharePct,
					tc.row.USDPerInstruction, tc.usd, tc.share, tc.perInstr)
			}
		})
	}
}

func TestByEffortWithoutACostLeavesCostUnset(t *testing.T) {
	records := []Record{{At: time.Now(), SessionID: "a", Kind: KindPrompt}, {At: time.Now(), SessionID: "a", Kind: KindAssistantTurn, Model: "m", Effort: "low"}}
	for _, r := range ByEffort(records) {
		if r.APIEquivalentUSD != 0 || r.SharePct != 0 || r.USDPerInstruction != 0 {
			t.Errorf("cost without a cost source: %+v", r)
		}
	}
}

func TestUnitCostsSumTheTurnsInsideEachUnit(t *testing.T) {
	base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	turns := map[string][]TurnCost{"s": {
		{At: base.Add(time.Minute), USD: 1},
		{At: base.Add(2 * time.Minute), USD: 2},
		{At: base.Add(time.Hour), USD: 4}, // after the unit ends
	}}
	u := Unit{SessionID: "s", Start: base, LastActivity: base.Add(5 * time.Minute)}
	if got := UnitCosts(turns)(u); math.Abs(got-3) > 1e-9 {
		t.Errorf("unit cost = %v, want 3", got)
	}
	if got := UnitCosts(turns)(Unit{SessionID: "other", Start: base, LastActivity: base}); got != 0 {
		t.Errorf("unit with no session turns costs %v", got)
	}
}
