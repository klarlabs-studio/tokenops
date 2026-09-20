// Package freshness answers whether TokenOps is still observing what it
// was asked to observe.
//
// Liveness is not the same question. `/healthz` reports that the daemon
// is running, and it says so deliberately — a process can be perfectly
// alive while every reader inside it has stopped producing data. The
// incident this generalises from ran 27 days with `tokenops status`
// reporting healthy throughout, because the only thing it checked was
// whether the program was up.
//
// The judgement needs three independent facts, and conflating any two of
// them produces a wrong answer that looks confident:
//
//   - what TokenOps ingested — silence here means nothing on its own;
//   - what the origin holds — a vendor the operator stopped using has
//     nothing to ingest, and calling that a fault is how an alarm becomes
//     one nobody reads;
//   - whether the reader is still succeeding — a poller being refused
//     every minute produces exactly the same silence as an unused vendor,
//     and is the opposite situation. One machine logged 3,019 consecutive
//     failures into a file nobody read.
//
// Assess is pure: every fact is injected, so the rules are testable
// without a database, a network or a clock.
package freshness

import (
	"sort"
	"time"
)

// DefaultWindow is the lookback used when a caller names none.
//
// 48h is deliberately generous: it tolerates a weekend of not touching a
// vendor while still catching a reader that silently died.
const DefaultWindow = 48 * time.Hour

// Severity grades how much attention a source needs. A warning that
// never escalates reads the same on day two and day twenty-seven, which
// is how a month-long blackout went unnoticed.
const (
	SeverityOK       = "ok"
	SeverityWarning  = "warning"
	SeverityDegraded = "degraded"
	SeverityCritical = "critical"
)

// Escalation thresholds, measured from the last ingested event.
const (
	degradedAfter = 7 * 24 * time.Hour
	criticalAfter = 14 * 24 * time.Hour
)

// State names what is happening to a source, as a closed set surfaces
// can dispatch on rather than parsing a sentence.
type State string

const (
	// StateHealthy — data arrived inside the window.
	StateHealthy State = "healthy"
	// StateIdle — quiet, and legitimately so: the origin has nothing
	// newer, or the last poll succeeded and found nothing new.
	StateIdle State = "idle"
	// StateStale — quiet while there is data to be had, or while nothing
	// can confirm otherwise. Something that should be reading is not.
	StateStale State = "stale"
	// StateFailing — the reader is alive and being refused. Distinct from
	// stale because the remedy is a credential, not a restart.
	StateFailing State = "failing"
)

// Source identifies one configured ingestion source.
type Source struct {
	// Name is what an operator calls it.
	Name string
	// Tag is the value the event store records in its source column.
	Tag string
}

// Origin is what a source's upstream holds, read at its own end: the
// newest transcript on disk, the newest row in a client's own store.
//
// Known=false means the origin could not be read. That is an unknown,
// not a clean bill of health, and the time-based judgement stands.
type Origin struct {
	At    time.Time
	Known bool
}

// Poll is what a poller knows about its own recent attempts.
//
// LastSuccessAt is the field that does the work. Without it a live
// poller holding an expired credential and a vendor nobody is using look
// identical from event counts: both produce nothing.
type Poll struct {
	// LastAttemptAt is when the poller last tried, successfully or not.
	LastAttemptAt time.Time
	// LastSuccessAt is when it last completed a poll without error.
	LastSuccessAt time.Time
	// LastError is the most recent failure, nil when the last attempt
	// succeeded.
	LastError error
	// LastErrorAt is when that failure happened.
	LastErrorAt time.Time
}

// attempted reports whether this record describes a poller that has run
// at all. The zero Poll means "no poller reported in", which is not the
// same as "the poller never succeeded".
func (p Poll) attempted() bool {
	return !p.LastAttemptAt.IsZero() || !p.LastSuccessAt.IsZero() || p.LastError != nil
}

// Inputs are the facts Assess judges. Maps are keyed by Source.Tag; a
// missing entry means "not known", never "zero".
type Inputs struct {
	Now     time.Time
	Window  time.Duration
	Sources []Source
	// EventsInWindow is how many events each source produced in the
	// window.
	EventsInWindow map[string]int64
	// LastEventAt is the most recent event per source. A missing entry
	// means nothing was ever ingested from it.
	LastEventAt map[string]time.Time
	// OriginNewest is what each source's upstream holds. Optional.
	OriginNewest map[string]Origin
	// Polls is what each source's poller reports about itself. Optional:
	// file readers have no poller.
	Polls map[string]Poll
	// Stopped names readers that have exited, and why.
	//
	// This is a stronger fact than a failed poll: a reader that is gone
	// is not coming back on its own. It only became knowable once the
	// daemon's pollers ran under a supervisor — as bare goroutines, one
	// dying logged a line and vanished while every surface went on
	// reporting health.
	Stopped map[string]error
}

// Report is the assessment of one source, as data.
//
// The check this replaces returned only the stale sources, rendered as a
// warning sentence. That made "everything is fine" and "nothing is
// configured" the same empty list, and left every consumer — the CLI
// table, the MCP tool, a dashboard — to parse prose or go without.
type Report struct {
	Name string `json:"name"`
	Tag  string `json:"tag"`

	State    State  `json:"state"`
	Severity string `json:"severity"`

	// LastEventAt is when this source last produced an event. Zero when
	// it never has; see NeverSeen.
	LastEventAt time.Time `json:"last_event_at,omitzero"`
	// NeverSeen distinguishes "has not produced since we started
	// watching" from "stopped producing", which need different advice.
	NeverSeen bool `json:"never_seen,omitempty"`
	// SilentFor is how long since the last event. Zero when never seen.
	SilentFor time.Duration `json:"silent_for,omitempty"`
	// EventsInWindow is what it produced in the window.
	EventsInWindow int64 `json:"events_in_window"`

	// LastPollAt is when its reader last tried. Zero for sources with no
	// poller, or none that reported in.
	LastPollAt time.Time `json:"last_poll_at,omitzero"`
	// LastSuccessfulPollAt is when its reader last succeeded. A recent
	// value with no events means the vendor has nothing new — which is
	// health, not silence.
	LastSuccessfulPollAt time.Time `json:"last_successful_poll_at,omitzero"`
	// LastError is the reader's most recent failure, rendered.
	LastError string `json:"last_error,omitempty"`
	// LastErrorAt is when that failure happened.
	LastErrorAt time.Time `json:"last_error_at,omitzero"`

	// OriginNewestAt is the newest data the upstream holds, when it could
	// be read.
	OriginNewestAt time.Time `json:"origin_newest_at,omitzero"`
	// ReaderStopped reports that this source's reader exited. Events may
	// still be inside the window — they are from before it died, and
	// reporting healthy on the strength of them is the delay that lets
	// an outage run for weeks.
	ReaderStopped bool `json:"reader_stopped,omitempty"`

	// Window is the lookback this report was computed over.
	Window time.Duration `json:"window"`
}

// Healthy reports whether this source needs no attention.
func (r Report) Healthy() bool {
	return r.State == StateHealthy || r.State == StateIdle
}

// Assess judges every configured source and returns one report each,
// ordered by tag so a table does not reshuffle between refreshes.
//
// Healthy sources are included. A caller that wants only the problems
// can filter; a caller that wants to show an operator what is being
// watched could not previously do so at all.
func Assess(in Inputs) []Report {
	if len(in.Sources) == 0 {
		return nil
	}
	window := in.Window
	if window <= 0 {
		window = DefaultWindow
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}

	out := make([]Report, 0, len(in.Sources))
	for _, s := range in.Sources {
		out = append(out, assessOne(s, in, window, now))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out
}

func assessOne(s Source, in Inputs, window time.Duration, now time.Time) Report {
	r := Report{
		Name:           s.Name,
		Tag:            s.Tag,
		Window:         window,
		EventsInWindow: in.EventsInWindow[s.Tag],
	}

	last, seen := in.LastEventAt[s.Tag]
	switch {
	case !seen || last.IsZero():
		r.NeverSeen = true
	default:
		r.LastEventAt = last
		if now.After(last) {
			r.SilentFor = now.Sub(last)
		}
	}

	origin, hasOrigin := in.OriginNewest[s.Tag]
	if hasOrigin && origin.Known {
		r.OriginNewestAt = origin.At
	}

	if err, stopped := in.Stopped[s.Tag]; stopped && err != nil {
		r.LastError = err.Error()
		r.ReaderStopped = true
	}

	poll, hasPoll := in.Polls[s.Tag]
	if hasPoll {
		r.LastPollAt = poll.LastAttemptAt
		r.LastSuccessfulPollAt = poll.LastSuccessAt
		r.LastErrorAt = poll.LastErrorAt
		if poll.LastError != nil && !r.ReaderStopped {
			r.LastError = poll.LastError.Error()
		}
	}

	r.State = stateOf(r, origin, hasOrigin, poll, hasPoll, window, now)
	r.Severity = severityOf(r)
	return r
}

// stateOf applies the three facts in the order that makes each one
// meaningful.
func stateOf(r Report, origin Origin, hasOrigin bool, poll Poll, hasPoll bool, window time.Duration, now time.Time) State {
	// A reader that exited outranks everything else, including events
	// inside the window: those are from before it died.
	if r.ReaderStopped {
		return StateFailing
	}
	if r.EventsInWindow > 0 {
		return StateHealthy
	}

	// A reader that is alive and being refused is the case a count of
	// zero cannot distinguish from an unused vendor, and it is checked
	// first because its remedy — a credential — is nothing like the
	// remedy for either of the others.
	if hasPoll && poll.attempted() && poll.LastError != nil {
		return StateFailing
	}

	// A poll that succeeded inside the window proves the reader works and
	// the vendor had nothing to give.
	if hasPoll && !poll.LastSuccessAt.IsZero() && poll.LastSuccessAt.After(now.Add(-window)) {
		return StateIdle
	}

	// The origin holding nothing newer than the window means there was
	// nothing to miss. The comparison is against the window rather than
	// against the last ingested event on purpose: a transcript's mtime
	// trails its last entry by however long the client held the file
	// open, so "newer than the last event" is true by seconds on a source
	// that is perfectly caught up.
	if hasOrigin && origin.Known {
		if origin.At.IsZero() || origin.At.Before(now.Add(-window)) {
			return StateIdle
		}
		return StateStale
	}

	// Nothing could confirm the silence is benign. An unknown is not a
	// clean bill of health.
	return StateStale
}

// severityOf escalates with the size of the gap, so repeated warnings
// stop reading identically as an outage lengthens.
func severityOf(r Report) string {
	if r.State == StateHealthy || r.State == StateIdle {
		return SeverityOK
	}
	switch {
	case r.SilentFor >= criticalAfter:
		return SeverityCritical
	case r.SilentFor >= degradedAfter:
		return SeverityDegraded
	case r.ReaderStopped:
		// A dead reader has no gap yet, and will never close the one it
		// is about to open. Grading it alongside a source that is merely
		// quiet understates it.
		return SeverityDegraded
	case r.NeverSeen:
		// Never having produced anything is not a lengthening outage; it
		// is usually a source that was enabled and never wired up.
		return SeverityWarning
	default:
		return SeverityWarning
	}
}
