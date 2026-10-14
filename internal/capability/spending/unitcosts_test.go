package spending

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/uniteconomics"
)

// The scorecard shows the headline unit costs: per commit, per merged
// pull request and per achieved outcome, with their baseline and change.
func TestScorecardUnitCostsFromTheReport(t *testing.T) {
	seven, ten, change := 7.0, 10.0, -30.0
	r := uniteconomics.Report{
		Commits:  uniteconomics.Unit{Name: "commit"},
		PRs:      uniteconomics.Unit{Name: "merged_pr", ChangePct: &change},
		Outcomes: uniteconomics.Unit{Name: "outcome"},
		Achieved: uniteconomics.Unit{Name: "achieved_outcome"},
	}
	r.PRs.Current.Units, r.PRs.Current.PerUnitUSD = 2, &seven
	r.PRs.Baseline.Units, r.PRs.Baseline.PerUnitUSD = 1, &ten
	got := scorecardUnitCosts(r)
	if len(got) != 3 || got[0].Unit != "commit" || got[1].Unit != "merged PR" || got[2].Unit != "achieved outcome" {
		t.Fatalf("unit costs = %+v", got)
	}
	pr := got[1]
	if pr.Units != 2 || pr.PerUnitUSD != 7 || pr.BaselinePerUnitUSD == nil || *pr.BaselinePerUnitUSD != 10 || pr.ChangePct == nil || *pr.ChangePct != -30 {
		t.Errorf("merged PR = %+v", pr)
	}
	if got[0].Units != 0 || got[0].BaselinePerUnitUSD != nil {
		t.Errorf("commit with none = %+v", got[0])
	}
}
