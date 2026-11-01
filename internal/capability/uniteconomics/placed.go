package uniteconomics

import (
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/commits"
)

// Kinds of placed unit.
const (
	UnitCommit   = "commit"
	UnitMergedPR = "merged_pr"
)

// Placed is one unit of finished work in the period with the work placed
// on it, by the joins of the report: a commit (CommitMethod) or a merged
// pull request (PRMethod). Like the report's, a unit is one with agent
// work on it.
type Placed struct {
	Unit string
	// Root is the repository's directory; At when the unit landed.
	Root string
	At   time.Time
	commits.Cost
	// Reverted is set on a commit a revert of the operator's undid within
	// commits.RevertWindow.
	Reverted bool
}

// PlacedUnits are the commits and merged pull requests landed in
// [Since, Until), oldest first. Turns from before Since are placed too:
// Gather from a week before Since, the longest a pull request's work may
// precede it.
func (in Inputs) PlacedUnits() []Placed {
	var out []Placed
	for _, c := range commits.Attribute(BaselineSince(in.Since), in.Turns, in.Dirs, in.RootOf, in.CommitsOf).Commits {
		if in.period(c.At) == 1 {
			out = append(out, Placed{Unit: UnitCommit, Root: c.Root, At: c.At, Cost: c.Cost, Reverted: c.Reverted})
		}
	}
	byPR, _, _, _, _ := in.placePRs()
	for k, p := range byPR {
		if in.period(p.at) == 1 {
			out = append(out, Placed{Unit: UnitMergedPR, Root: k.root, At: p.at, Cost: p.rate.Cost})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		if out[i].Unit != out[j].Unit {
			return out[i].Unit < out[j].Unit
		}
		return out[i].Root < out[j].Root
	})
	return out
}
