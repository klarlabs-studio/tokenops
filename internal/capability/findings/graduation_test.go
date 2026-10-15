package findings

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
)

// A capability that meets its bar shows on glance as a suggestion with
// the command; one that does not stays out of the way.
func TestGraduationFinding(t *testing.T) {
	ready := savings.Step{Power: "waste", Promote: "tokenops coach set waste autonomous",
		Graduation: learning.Graduation{Capability: learning.CapabilityReadGuard, Stage: learning.StageAdvise, Next: learning.StageAct, Ready: true,
			Criteria: []learning.Criterion{{Name: "shadow calls", Want: "at least 50", Have: "55", Met: true}}}}
	notYet := savings.Step{Power: "context", Promote: "tokenops coach set context autonomous",
		Graduation: learning.Graduation{Capability: learning.CapabilityCompaction, Stage: learning.StageAdvise, Next: learning.StageAct}}
	got := graduations([]savings.Step{ready, notYet})
	if len(got) != 1 {
		t.Fatalf("findings = %+v", got)
	}
	if all := coaching(&coach.Report{Graduation: []savings.Step{ready, notYet}}, nil); !strings.Contains(all[len(all)-1].Title, "ready") {
		t.Fatalf("coaching findings miss the graduation: %+v", all)
	}
	f := got[0]
	if f.Title != "Read guard is ready to enforce" || f.Level != LevelInfo ||
		!strings.Contains(f.Action, "`tokenops coach set waste autonomous`") || !strings.Contains(f.Evidence, "shadow calls: 55") {
		t.Fatalf("finding = %+v", f)
	}
}
