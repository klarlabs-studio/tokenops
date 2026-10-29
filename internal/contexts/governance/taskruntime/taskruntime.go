// Package taskruntime says where a task's elapsed time went, as far as
// the transcripts record it, and no further.
//
// A task's interval — first instruction to last observed activity — is
// easy to reconstruct and easy to misread. It holds model requests, tool
// execution, the operator answering questions, the operator not having
// typed yet, and stretches nothing recorded at all. Reading the whole
// interval as "model time" attributes a lunch break to the model; summing
// intervals across tasks counts concurrent work twice and calls it
// waiting.
//
// So this package is a coverage report, not a tracer. It decomposes each
// task into the categories the transcripts actually carry, with each
// category's origin and how much of the task it accounts for, and leaves
// the rest as unattributed. A category a harness does not record is
// unknown — never zero — and an instant is assigned to exactly one
// category, so the parts always add up to the observed interval and
// nothing is counted twice.
//
// The evidence/belief line is kept explicit: completion is what the
// harness recorded, not whether the work was right; the friction count is
// a heuristic and says so; nothing here claims a task succeeded.
package taskruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
)

// Status is what the transcript says about how a task ended.
type Status string

// Statuses.
const (
	// StatusCompleted: the harness recorded the end of the task's last
	// answered turn. Completion of the turn, not success of the work.
	StatusCompleted Status = "completed"
	// StatusLastObserved: no completion was recorded; the interval runs
	// to the last activity the transcript holds.
	StatusLastObserved Status = "last_observed"
	// StatusOpen: no completion was recorded and the session was active
	// within the idle gap of now. The task may still be running, so its
	// interval is right-censored and is no runtime sample.
	StatusOpen Status = "open"
)

// Category is one way a task's time was spent.
type Category string

// Categories, in the precedence an instant is assigned by when several
// recorded intervals cover it: a person waited, a tool ran, a model
// answered, nobody had typed yet; whatever is left is unattributed.
const (
	CategoryApprovalWait  Category = "approval_wait"
	CategoryToolExecution Category = "tool_execution"
	CategoryModelRequest  Category = "model_request"
	CategoryIdle          Category = "idle"
	CategoryUnattributed  Category = "unattributed"
)

// Categories lists every category in report order.
var Categories = []Category{
	CategoryModelRequest, CategoryToolExecution, CategoryApprovalWait, CategoryIdle, CategoryUnattributed,
}

// Options tunes the analysis.
type Options struct {
	// IdleGap is the pause after which a session counts as no longer
	// active, for the open status. Zero takes story's default.
	IdleGap time.Duration
	// MaxModelSpan is the longest model span attributed to the model. A
	// longer one is far more likely a suspended machine or a queued
	// request than generation, so its time stays unattributed. Zero takes
	// the idle gap.
	MaxModelSpan time.Duration
	// Now is the analysis time, for the open status.
	Now time.Time
}

func (o Options) idleGap() time.Duration {
	if o.IdleGap > 0 {
		return o.IdleGap
	}
	return story.DefaultIdleGap
}

func (o Options) maxModelSpan() time.Duration {
	if o.MaxModelSpan > 0 {
		return o.MaxModelSpan
	}
	return o.idleGap()
}

// CategoryTime is one category's share of a task.
type CategoryTime struct {
	Category Category `json:"category"`
	// Seconds is the time assigned to the category; null when the
	// harness does not record it.
	Seconds *float64 `json:"seconds"`
	// Share is Seconds over the observed interval; null when either is
	// unknown.
	Share   *float64            `json:"share_of_observed"`
	Quality measurement.Quality `json:"quality"`
	// Origin names what the figure was read from, or why it is unknown.
	Origin string `json:"origin"`
	// Complete is false when the figure is known to miss time of its
	// kind: a lower bound, not the whole.
	Complete bool   `json:"complete"`
	Caveat   string `json:"caveat,omitempty"`
	// Timed and Seen are the category's coverage: how many of the
	// intervals of its kind the transcript recorded both ends of.
	Timed int `json:"timed,omitempty"`
	Seen  int `json:"seen,omitempty"`
}

// Tools is a task's tool execution, both ways it can be measured.
type Tools struct {
	Calls int `json:"calls"`
	// Matched calls have a recorded result; Unmatched have no end.
	Matched   int `json:"matched"`
	Unmatched int `json:"unmatched"`
	// CriticalPathSeconds is the wall time at least one tool was
	// running: what the task waited on. ResourceSeconds sums every
	// call's span, so concurrent calls count once each: what the tools
	// used. They differ exactly by the overlap. Null when no call was
	// timed.
	CriticalPathSeconds *float64 `json:"critical_path_seconds"`
	ResourceSeconds     *float64 `json:"resource_seconds"`
	MaxConcurrent       int      `json:"max_concurrent"`
}

// Heuristic is what the transcript suggests about the work, labelled as
// a guess.
type Heuristic struct {
	// Frictions counts the story's friction signals: rework, rejection,
	// interruption, repeated instructions.
	Frictions int `json:"frictions"`
	// Clean is no friction signal at all. It is not success.
	Clean bool   `json:"clean"`
	Label string `json:"label"`
}

// Task is one task's runtime, derived figures only: no instruction text,
// file path or session id.
type Task struct {
	// Key identifies the task stably across runs; AttemptKeys identify
	// each instruction in it. Both are hashes.
	Key         string   `json:"task_key"`
	AttemptKeys []string `json:"attempt_keys"`
	Harness     string   `json:"harness"`
	Model       string   `json:"model,omitempty"`
	Role        string   `json:"role"`
	Start       string   `json:"start"`
	// Boundary is what started the task; BoundaryOrigin says it was
	// inferred, and from what.
	Boundary       string `json:"boundary"`
	BoundaryOrigin string `json:"boundary_origin"`

	Status         Status `json:"status"`
	StatusEvidence string `json:"status_evidence"`

	Instructions int `json:"instructions"`
	// Unanswered counts instructions with no turn after them.
	Unanswered int `json:"unanswered"`
	Turns      int `json:"turns"`

	// ObservedSeconds is first instruction to last observed activity.
	// Null for a task no turn answered: it has no runtime sample.
	ObservedSeconds *float64 `json:"observed_seconds"`
	ObservedNote    string   `json:"observed_note,omitempty"`

	// HarnessTurnSeconds sums the turn durations the harness recorded,
	// over HarnessTurnsRecorded of the task's answered instructions.
	HarnessTurnSeconds   *float64 `json:"harness_turn_seconds"`
	HarnessTurnsRecorded int      `json:"harness_turns_recorded"`
	// HarnessTTFTSeconds is the first turn's time to first token as the
	// harness recorded it; null where it records none. Never inferred.
	HarnessTTFTSeconds *float64 `json:"harness_ttft_seconds"`

	Categories []CategoryTime `json:"categories"`
	Tools      Tools          `json:"tools"`
	Heuristic  Heuristic      `json:"heuristic"`
	// Verified is whether anything independently checked the result.
	// Transcripts cannot say; it is always "unknown" here.
	Verified string `json:"verified_success"`
	// Usage is what the task's recorded turns used and cost, from the
	// event store; null when no recorded turn fell in the task.
	Usage *Usage `json:"usage"`

	// sessionKey is kept for grouping open status; never exported.
	sessionKey string
	// lastActivity is the task's last observed entry.
	lastActivity time.Time
}

// Analyse decomposes tasks, in the order given. Pass every task of the
// window, before any limit, so a session's last task can be told apart.
func Analyse(tasks []story.Task, opts Options) []Task {
	last := map[string]time.Time{}
	for _, t := range tasks {
		if a := lastActivity(t); a.After(last[t.SessionID]) {
			last[t.SessionID] = a
		}
	}
	out := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, analyseOne(t, last[t.SessionID], opts))
	}
	return out
}

func lastActivity(t story.Task) time.Time {
	a := t.Start
	for _, u := range t.Units {
		if u.LastActivity.After(a) {
			a = u.LastActivity
		}
		for _, s := range u.Spans {
			if s.End.After(a) {
				a = s.End
			}
		}
	}
	return a
}

func analyseOne(t story.Task, sessionLast time.Time, opts Options) Task {
	out := Task{
		Key:            key("task", t.SessionID, t.Start),
		Harness:        harnessOf(t),
		Model:          modelOf(t),
		Role:           roleOf(t),
		Start:          t.Start.UTC().Format(time.RFC3339),
		Boundary:       string(t.Boundary),
		BoundaryOrigin: boundaryOrigin(t.Boundary),
		Instructions:   len(t.Units),
		Turns:          t.Turns(),
		Verified:       "unknown",
		sessionKey:     t.SessionID,
		lastActivity:   lastActivity(t),
	}
	for _, u := range t.Units {
		out.AttemptKeys = append(out.AttemptKeys, key("attempt", t.SessionID, u.Start))
		if u.Turns == 0 {
			out.Unanswered++
		}
	}
	out.Status, out.StatusEvidence = status(t, out.lastActivity, sessionLast, opts)
	frictions := len(t.Frictions())
	out.Heuristic = Heuristic{Frictions: frictions, Clean: frictions == 0,
		Label: "heuristic: friction signals read from the transcript; not a verdict on the work"}

	harnessTiming(&out, t)
	out.Tools = tools(t)
	if out.Turns == 0 || !out.lastActivity.After(t.Start) {
		out.ObservedNote = "no turn answered this task, so it has no runtime sample"
		out.Categories = unknownCategories("the task has no observed interval")
		return out
	}
	observed := out.lastActivity.Sub(t.Start).Seconds()
	out.ObservedSeconds = &observed
	out.Categories = decompose(t, out.Harness, out.lastActivity, opts)
	return out
}

// key hashes what identifies a task or an attempt, so the export carries
// a stable id and no session id.
func key(kind, session string, at time.Time) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d", kind, session, at.UTC().UnixNano()))
	return hex.EncodeToString(sum[:8])
}

func harnessOf(t story.Task) string {
	for _, u := range t.Units {
		if u.Harness != "" {
			return u.Harness
		}
	}
	return "unknown"
}

// modelOf is the model that served most of the task's turns.
func modelOf(t story.Task) string {
	turns := map[string]int{}
	for _, u := range t.Units {
		if u.Model != "" {
			turns[u.Model] += u.Turns
		}
	}
	best, n := "", -1
	for m, c := range turns {
		if c > n || (c == n && m < best) {
			best, n = m, c
		}
	}
	return best
}

// roleOf is the task's role: its first instruction's, since a task opens
// on what was asked.
func roleOf(t story.Task) string {
	if len(t.Units) == 0 {
		return agentdx.RolePrimary
	}
	return agentdx.RoleOf(t.Units[0].Role, t.Units[0].Model)
}

func boundaryOrigin(b story.Boundary) string {
	switch b {
	case story.BoundarySessionStart:
		return "inferred: the first instruction of a session"
	case story.BoundaryIdle:
		return "inferred: a pause longer than the idle gap before the instruction"
	}
	return "inferred: " + string(b)
}

// status reads how the task ended from what the harness recorded.
func status(t story.Task, last, sessionLast time.Time, opts Options) (Status, string) {
	answered := -1
	for i, u := range t.Units {
		if u.Turns > 0 {
			answered = i
		}
	}
	lastUnanswered := len(t.Units) > 0 && t.Units[len(t.Units)-1].Turns == 0
	if answered >= 0 && !lastUnanswered && t.Units[answered].Completed {
		return StatusCompleted, "the harness recorded the end of the last turn; that says the turn ended, not that the work was right"
	}
	if !opts.Now.IsZero() && last.Equal(sessionLast) && opts.Now.Sub(last) < opts.idleGap() {
		return StatusOpen, "no completion recorded and the session was active within the idle gap: the task may still be running (right-censored)"
	}
	if lastUnanswered {
		return StatusLastObserved, "the last instruction was never answered; the interval ends at the last observed activity"
	}
	return StatusLastObserved, "no completion recorded; the interval ends at the last observed activity"
}

// harnessTiming copies the turn timing the harness itself recorded.
func harnessTiming(out *Task, t story.Task) {
	var ms int64
	for _, u := range t.Units {
		if u.HarnessTurns > 0 {
			ms += u.HarnessTurnMs
			out.HarnessTurnsRecorded += u.HarnessTurns
		}
	}
	if out.HarnessTurnsRecorded > 0 {
		s := float64(ms) / 1000
		out.HarnessTurnSeconds = &s
	}
	for _, u := range t.Units {
		if u.Turns == 0 {
			continue
		}
		if u.TTFTRecorded {
			s := float64(u.TTFTMs) / 1000
			out.HarnessTTFTSeconds = &s
		}
		break
	}
}

// tools measures tool execution as critical path and as resource time.
func tools(t story.Task) Tools {
	var out Tools
	var spans []interval
	for _, u := range t.Units {
		out.Calls += u.ToolCalls
		out.Matched += u.MatchedToolCalls
		for _, s := range u.Spans {
			if s.Kind == agentdx.SpanTool {
				spans = append(spans, interval{s.Start, s.End})
			}
		}
	}
	out.Unmatched = out.Calls - out.Matched
	if len(spans) == 0 {
		return out
	}
	var resource time.Duration
	for _, s := range spans {
		resource += s.end.Sub(s.start)
	}
	critical, peak := union(spans)
	cs, rs := critical.Seconds(), resource.Seconds()
	out.CriticalPathSeconds, out.ResourceSeconds, out.MaxConcurrent = &cs, &rs, peak
	return out
}

type interval struct{ start, end time.Time }

// union is the length covered by at least one interval, and the most
// intervals covering one instant.
func union(spans []interval) (time.Duration, int) {
	type edge struct {
		at    time.Time
		delta int
	}
	edges := make([]edge, 0, 2*len(spans))
	for _, s := range spans {
		edges = append(edges, edge{s.start, 1}, edge{s.end, -1})
	}
	sort.Slice(edges, func(i, j int) bool {
		if !edges[i].at.Equal(edges[j].at) {
			return edges[i].at.Before(edges[j].at)
		}
		return edges[i].delta < edges[j].delta
	})
	var total time.Duration
	depth, peak := 0, 0
	var from time.Time
	for _, e := range edges {
		if depth > 0 {
			total += e.at.Sub(from)
		}
		depth += e.delta
		if depth > peak {
			peak = depth
		}
		from = e.at
	}
	return total, peak
}

func unknownCategories(reason string) []CategoryTime {
	out := make([]CategoryTime, 0, len(Categories))
	for _, c := range Categories {
		out = append(out, CategoryTime{Category: c, Quality: measurement.QualityUnknown, Origin: "unknown: " + reason})
	}
	return out
}
