package agentdx

import (
	"testing"

	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// The effort levels a team upload's model rows may carry are the levels
// this package ranks, in its order: a level added here must be added to
// teamwire.EffortLevels, or it crosses as "other".
func TestTeamwireEffortLevelsAreRanked(t *testing.T) {
	if len(teamwire.EffortLevels) != len(effortOrder) {
		t.Fatalf("teamwire.EffortLevels %v; effortOrder %v", teamwire.EffortLevels, effortOrder)
	}
	for i, l := range teamwire.EffortLevels {
		if rank, ok := effortOrder[l]; !ok || rank != i {
			t.Errorf("teamwire.EffortLevels[%d] = %q; effortOrder ranks it %d (known %v)", i, l, rank, ok)
		}
	}
}
