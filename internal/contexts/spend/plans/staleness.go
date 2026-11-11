package plans

import (
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/heartbeat"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A stored reading is what its reader last saw. Whether that still holds
// depends on the reader: one that polls on a schedule replaces its reading
// whether or not anyone works, so an old reading from it means the reader
// has stopped, and the share it shows is no longer known to be current.
// One that reads as work happens leaves an old reading behind whenever its
// client is idle, and nothing has changed since.
//
// Every reader of a stored reading asks ReadingStale, so "has this
// reading's reader stopped" has one answer.

// workDriven are the sources that read as work happens: Claude Code's
// status line and Codex's rollouts. An old reading from one is an idle
// client, not a reader that stopped.
var workDriven = map[string]bool{
	"claude-code-statusline": true,
	"codex-jsonl":            true,
}

// unstampedGoodFor is how long a reading stands that neither says how long
// (heartbeat.Attr) nor comes from a work-driven source: one stored before
// readings said, or by a reader that does not say. Past it the reading is
// stale rather than silently current.
const unstampedGoodFor = time.Hour

// ReadingStale reports whether e's reader has stopped replacing it by now:
// e is older than it says it stands for. A reading that does not say is
// never stale from a work-driven source, and stale after an hour from any
// other.
func ReadingStale(e *eventschema.Envelope, now time.Time) bool {
	if e == nil {
		return false
	}
	age := now.Sub(e.Timestamp)
	if limit, ok := heartbeat.FreshFor(e.Attributes); ok {
		return age > limit
	}
	if workDriven[e.Source] {
		return false
	}
	return age > unstampedGoodFor
}
