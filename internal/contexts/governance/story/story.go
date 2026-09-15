// Package story turns agent transcripts into an account of what happened:
// what you asked for, what the agent did about it, what it cost, and
// where it went sideways.
//
// The unit is a task — a run of consecutive instructions that belong to
// one piece of work. Boundaries are inferred rather than demanded.
// `tokenops task start|done` exists and marks them exactly, but a feature
// that only works for people who adopt a new habit does nothing on the
// history everyone already has, so inference is the default and explicit
// marks are the override.
//
// Prompt text is read at scan time and never persisted. A story is
// assembled from transcripts on each run and exists for as long as it
// takes to print it.
package story

import (
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

// DefaultIdleGap is how long a pause has to be before the work after it
// counts as a different task.
//
// Ten minutes is long enough to survive reading a diff, running a build,
// or answering a message, and short enough that coming back after lunch
// starts a new story. It is a heuristic and it will sometimes be wrong,
// which is why Task.Reason says what split it — a boundary you can see
// is one you can argue with.
const DefaultIdleGap = 10 * time.Minute

// Boundary explains why a task began where it did.
type Boundary string

const (
	// BoundarySessionStart opens the first task of a session. Tasks never
	// span sessions: a new session is a new context window, and work that
	// survives one is being started again rather than continued.
	BoundarySessionStart Boundary = "session start"
	// BoundaryIdle is a pause longer than the idle gap.
	BoundaryIdle Boundary = "idle gap"
)

// Task is one piece of work: consecutive instructions and everything the
// agent did answering them.
type Task struct {
	Title     string
	SessionID string
	Provider  string
	Start     time.Time
	End       time.Time
	Units     []agentdx.Unit
	Boundary  Boundary
}

// Options tunes grouping.
type Options struct {
	// IdleGap overrides DefaultIdleGap. Zero uses the default; negative
	// disables idle splitting, so a task is exactly a session.
	IdleGap time.Duration
}

func (o Options) idleGap() time.Duration {
	switch {
	case o.IdleGap < 0:
		return 0
	case o.IdleGap == 0:
		return DefaultIdleGap
	default:
		return o.IdleGap
	}
}

// Group clusters units into tasks.
//
// Units are partitioned by session before any boundary is inferred.
// Concurrent agent sessions are ordinary — two terminals, two projects —
// and their transcripts interleave in wall-clock time. Walking a global
// time order would then see the session change at almost every unit and
// split every instruction into its own task, which is the shape a first
// implementation of this produced on real data. Tasks never span
// sessions by design, so partitioning first is both correct and simpler
// than defending against the interleaving later.
func Group(units []agentdx.Unit, opts Options) []Task {
	gap := opts.idleGap()
	sessions := partitionBySession(units)
	out := make([]Task, 0, len(sessions))
	for _, bySession := range sessions {
		out = append(out, groupOne(bySession, gap)...)
	}
	sortByStart(out)
	for i := range out {
		out[i].Title = titleFor(out[i])
		// A task whose every unit went unanswered has no end; fall back to
		// its start so a duration of zero reads as zero rather than as a
		// negative interval against the zero time.
		if out[i].End.IsZero() {
			out[i].End = out[i].Start
		}
	}
	return out
}

// partitionBySession splits units per session, preserving each session's
// own chronological order and the order in which sessions first appear.
func partitionBySession(units []agentdx.Unit) [][]agentdx.Unit {
	var order []string
	byID := map[string][]agentdx.Unit{}
	for _, u := range units {
		if _, seen := byID[u.SessionID]; !seen {
			order = append(order, u.SessionID)
		}
		byID[u.SessionID] = append(byID[u.SessionID], u)
	}
	out := make([][]agentdx.Unit, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

func sortByStart(ts []Task) {
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].Start.Before(ts[j].Start) })
}

// groupOne walks a single session's units, splitting on idle gaps.
func groupOne(units []agentdx.Unit, gap time.Duration) []Task {
	var out []Task
	for i, u := range units {
		boundary, isNew := boundaryFor(i, u, units, gap)
		if isNew {
			out = append(out, Task{
				SessionID: u.SessionID,
				Provider:  u.Provider,
				Start:     u.Start,
				Boundary:  boundary,
			})
		}
		t := &out[len(out)-1]
		t.Units = append(t.Units, u)
		if end := u.End; end.After(t.End) {
			t.End = end
		}
		if t.Provider == "" {
			t.Provider = u.Provider
		}
	}
	return out
}

func boundaryFor(i int, u agentdx.Unit, units []agentdx.Unit, gap time.Duration) (Boundary, bool) {
	if i == 0 {
		return BoundarySessionStart, true
	}
	// An instruction that only means something in reference to the work in
	// flight continues it, however long the pause was. This is what keeps
	// a 20-minute wait followed by "Go" from becoming a task called "Go".
	if IsContinuation(u) {
		return "", false
	}
	prev := units[i-1]
	if gap > 0 {
		// Measure the pause from when the agent stopped working, not from
		// when the previous instruction was typed: a unit that ran for an
		// hour has not left the operator idle for an hour.
		last := prev.End
		if last.IsZero() {
			last = prev.Start
		}
		if u.Start.Sub(last) > gap {
			return BoundaryIdle, true
		}
	}
	return "", false
}

// titleFor names a task after the first instruction in it that carries
// work of its own, quoted rather than paraphrased: a title produced by a
// summariser can be wrong, and this one cannot.
//
// The opening instruction is not always the defining one. A task can open
// with "Go" — the operator answering a question from the previous turn —
// and the work it names arrives one instruction later. Titling from the
// opener produced "Go" or "Proceed" for 71% of tasks on real history.
func titleFor(t Task) string {
	if len(t.Units) == 0 {
		return "(empty)"
	}
	first := strings.TrimSpace(t.Units[firstSubstantive(t.Units)].Prompt)
	if first == "" {
		return "(no instruction text)"
	}
	if idx := strings.IndexByte(first, '\n'); idx >= 0 {
		first = strings.TrimSpace(first[:idx])
	}
	return truncate(first, 72)
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max-1])) + "…"
}

// Duration is wall-clock from the opening instruction to the agent's
// last turn.
func (t Task) Duration() time.Duration {
	if t.End.After(t.Start) {
		return t.End.Sub(t.Start)
	}
	return 0
}

// Instructions is how many times the operator had to say something.
func (t Task) Instructions() int { return len(t.Units) }

// Turns, ToolCalls and Tokens are the task's totals.
func (t Task) Turns() int {
	n := 0
	for _, u := range t.Units {
		n += u.Turns
	}
	return n
}

func (t Task) ToolCalls() int {
	n := 0
	for _, u := range t.Units {
		n += u.ToolCalls
	}
	return n
}

// ContextCarried sums the context re-sent across every turn. It
// double-counts by construction and is not a spend figure; PeakContext is
// the number to show a human.
func (t Task) ContextCarried() int64 {
	var n int64
	for _, u := range t.Units {
		n += u.Tokens
	}
	return n
}

// PeakContext is the largest context any single turn carried — how heavy
// the work got, counting the context once.
func (t Task) PeakContext() int64 {
	var n int64
	for _, u := range t.Units {
		if u.PeakContext > n {
			n = u.PeakContext
		}
	}
	return n
}

// Files are the distinct files edited across the task, in the order they
// were first touched.
func (t Task) Files() []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range t.Units {
		for _, f := range u.Files {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// Friction is one thing that went wrong, in the operator's terms.
type Friction struct {
	Kind string
	// Detail is the specific instance: which instruction, which file.
	Detail string
}

// Frictions reports where the task went sideways, in the order it
// happened. An empty result means the work ran clean, which is worth
// saying out loud — most accounts of agent sessions only ever surface
// problems, which makes a good session look identical to an unmeasured
// one.
func (t Task) Frictions() []Friction {
	var out []Friction
	for i, u := range t.Units {
		where := ordinal(i + 1)
		if u.Interrupted {
			out = append(out, Friction{
				Kind:   "interrupted",
				Detail: "you stopped the agent during the " + where + " instruction",
			})
		}
		if u.Rejected {
			out = append(out, Friction{
				Kind:   "rejected",
				Detail: "you told it the " + where + " answer was wrong",
			})
		}
		if u.Reworked {
			out = append(out, Friction{
				Kind:   "rework",
				Detail: "the " + where + " instruction edited the same file twice",
			})
		}
		if u.Escalated {
			out = append(out, Friction{
				Kind:   "delegated",
				Detail: "the " + where + " instruction was handed to a subagent",
			})
		}
	}
	return out
}

// Clean reports whether the task ran without friction.
func (t Task) Clean() bool { return len(t.Frictions()) == 0 }

func ordinal(n int) string {
	switch {
	case n%100 >= 11 && n%100 <= 13:
		return itoa(n) + "th"
	case n%10 == 1:
		return itoa(n) + "st"
	case n%10 == 2:
		return itoa(n) + "nd"
	case n%10 == 3:
		return itoa(n) + "rd"
	default:
		return itoa(n) + "th"
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
