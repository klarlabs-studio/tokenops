package uniteconomics

import (
	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// FailuresMethod says how the work that did not stand is found.
const FailuresMethod = "work placed on a commit of yours that a revert undid within 14 days (\"This reverts commit …\"), or run under an execution whose recorded outcome is not achieved; work that is both is counted once in the share"

// Failures is the period's work that did not stand: the expensive
// failures, which cost the same as work that did.
type Failures struct {
	Method string `json:"method"`
	// RevertedCommits counts the commits with work on them that a revert
	// undid; Reverted is that work.
	RevertedCommits int          `json:"reverted_commits"`
	Reverted        commits.Cost `json:"reverted"`
	// NotAchievedOutcomes counts the executions recorded as not achieved
	// with work under them; NotAchieved is that work.
	NotAchievedOutcomes int          `json:"not_achieved_outcomes"`
	NotAchieved         commits.Cost `json:"not_achieved"`
	// Share is the part of the period's API-equivalent value that went on
	// either.
	Share float64 `json:"share"`
}

// failures finds the period's work that did not stand.
func (in Inputs) failures(current []analytics.SessionTurn, total float64) Failures {
	f := Failures{Method: FailuresMethod}
	undone := commits.Reverted(in.CommitsOf)
	execs := judgedExecutions(in.Outcomes)
	revertedCommits := map[[2]string]bool{}
	notAchieved := map[*judged]bool{}
	var failed float64
	for _, t := range current {
		bad := false
		if placed, c := commits.Place(t, in.Dirs, in.RootOf, in.CommitsOf); placed == commits.OnCommit {
			k := [2]string{in.RootOf[in.Dirs[t.SessionID].CWD], c.SHA}
			if undone[k] {
				f.Reverted.Add(t)
				revertedCommits[k] = true
				bad = true
			}
		}
		if j := judgedFor(execs, t); j != nil && j.result == eventschema.OutcomeNotAchieved {
			f.NotAchieved.Add(t)
			notAchieved[j] = true
			bad = true
		}
		if bad {
			failed += t.APIEquivalentUSD
		}
	}
	f.RevertedCommits, f.NotAchievedOutcomes = len(revertedCommits), len(notAchieved)
	if total > 0 {
		f.Share = failed / total
	}
	return f
}
