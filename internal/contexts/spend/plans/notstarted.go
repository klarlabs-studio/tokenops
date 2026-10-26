package plans

import "time"

// notStartedTolerance is how far a reset may sit from a full window's
// length ahead of its reading and still count as that. The vendor computes
// it at the request, so the gap is seconds; five minutes absorbs the rest.
const notStartedTolerance = 5 * time.Minute

// notStartedMove is how far a reset must move between two readings for the
// window to be read as drifting rather than fixed.
const notStartedMove = time.Minute

// notStarted reports whether cur, read after prev, is a window no request
// has started yet. Codex reports such a window at 0% with a reset a full
// window's length from each reading, and that reset moves with every
// reading. A window that has begun keeps one reset from its first request.
// Both readings must show it, so one reading alone never decides.
func notStarted(cur, prev VendorWindow) bool {
	if cur.UsedPct > 0 || prev.UsedPct > 0 || cur.Duration <= 0 || cur.Duration != prev.Duration {
		return false
	}
	if !prev.ObservedAt.Before(cur.ObservedAt) || !fullLengthAhead(cur) || !fullLengthAhead(prev) {
		return false
	}
	return absDuration(cur.ResetsAt.Sub(prev.ResetsAt)) > notStartedMove
}

// fullLengthAhead is whether w's reset is a full window's length after the
// reading that reported it.
func fullLengthAhead(w VendorWindow) bool {
	if w.ResetsAt.IsZero() || w.ObservedAt.IsZero() {
		return false
	}
	return absDuration(w.ResetsAt.Sub(w.ObservedAt)-w.Duration) <= notStartedTolerance
}

// markNotStarted flags w when the reading before it shows the same window
// not started, and drops the reset a not-started window does not have.
func markNotStarted(w *VendorWindow, before []VendorWindow) {
	for _, b := range before {
		if b.Name == w.Name && b.VendorLabel == w.VendorLabel && notStarted(*w, b) {
			w.NotStarted, w.ResetsAt, w.ResetsIn = true, time.Time{}, ""
			return
		}
	}
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
