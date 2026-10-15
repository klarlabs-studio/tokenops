package scorecard

import (
	"context"
	"strings"
	"testing"
)

// Tagged spend is shown with spend attribution: how much of the period's
// spend the allocation rules file under a project, client or cost centre.
func TestScorecardCarriesAllocationCoverage(t *testing.T) {
	sc := Build(context.Background(), nil, BuildParams{SACPctOverride: 95, Allocation: &AllocationCoverage{
		Rules: 2, TaggedPct: 62.5, ByKeyPct: map[string]float64{"project": 62.5, "client": 40, "cost_center": 0},
	}})
	text := sc.String()
	sac := strings.Index(text, "SAC")
	tags := strings.Index(text, "allocation tags on 62.5% of spend (project 62%, client 40%)")
	if sac < 0 || tags < sac {
		t.Errorf("the allocation line is not under SAC:\n%s", text)
	}
	b, err := sc.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"allocation"`) || !strings.Contains(string(b), `"tagged_pct": 62.5`) {
		t.Errorf("JSON missing allocation: %s", b)
	}

	none := Build(context.Background(), nil, BuildParams{SACPctOverride: 95, Allocation: &AllocationCoverage{}})
	if strings.Contains(none.String(), "allocation tags") {
		t.Error("without rules there is no allocation line")
	}
}
