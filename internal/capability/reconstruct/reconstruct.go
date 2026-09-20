// Package reconstruct answers "what work was actually done", from the
// transcripts the agent produced anyway.
//
// It is the third capability under ADR 0004 Phase 4, and it exists
// because the architecture test refused the alternative. Reaching into
// the story grouper and the ontology adapter from inside `internal/cli`
// was a new direct adapter → domain import, and the ratchet fails on
// those — a capability written inside one adapter is one the other will
// write again, differently, which is how plan headroom came to sort its
// providers on one surface and not the other.
//
// Grouping transcript units into stories and translating those into
// Work, Execution and Outcome is one capability. Both the CLI's `story`
// command and the MCP story surface want it, and the knobs — the idle
// gap that decides what counts as one piece of work, the newest-first
// order, the limit — are exactly the kind of detail two independent
// implementations end up disagreeing about.
package reconstruct

import (
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
	"go.klarlabs.de/tokenops/internal/contexts/work/fromstory"
)

// Work is one reconstructed piece of work: the goal, who attempted it,
// the attempt, and what is known about the result.
//
// Aliased rather than copied so the ontology stays the single definition
// of these concepts, while adapters name the capability's type and do
// not reach past it. That is the whole point of the layer: an adapter
// that imports the domain directly is one that will grow its own
// orchestration there.
type Work = fromstory.Reconstructed

// Options tunes the reconstruction.
type Options struct {
	// IdleGap is the pause that starts a new piece of work. Zero takes
	// story's default; negative disables idle splitting, so a session is
	// one story.
	IdleGap time.Duration
	// Limit caps how many are returned, newest first. Zero means all,
	// matching the flag that feeds it.
	Limit int
}

// FromUnits groups transcript units into work and returns it newest
// first.
//
// Newest first because the work you are most likely asking about is the
// work you just did. The CLI did that itself; doing it here means a
// second surface cannot forget to.
func FromUnits(units []agentdx.Unit, opts Options) []Work {
	if len(units) == 0 {
		return nil
	}
	tasks := story.Group(units, story.Options{IdleGap: opts.IdleGap})
	reverse(tasks)
	if opts.Limit > 0 && len(tasks) > opts.Limit {
		tasks = tasks[:opts.Limit]
	}
	return fromstory.ConvertAll(tasks)
}

// FromTasks translates already-grouped stories, for a caller that has
// its own reason to group them.
func FromTasks(tasks []story.Task) []Work {
	return fromstory.ConvertAll(tasks)
}

func reverse(ts []story.Task) {
	for i, j := 0, len(ts)-1; i < j; i, j = i+1, j-1 {
		ts[i], ts[j] = ts[j], ts[i]
	}
}
