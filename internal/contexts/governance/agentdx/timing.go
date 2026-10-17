package agentdx

import (
	"strings"
	"time"
)

// unitTiming collects the intervals one unit's transcript entries record.
//
// It records only what the transcript states. A tool span runs from the
// entry that made the call to the entry that carried its result, paired by
// id; a call with no result has no end and no span. A model span runs
// from the latest input written before a request (the instruction, or a
// tool result) to the last entry that request wrote, and exists only when
// the client groups entries by request id. Nothing here estimates when a
// response started streaming: transcripts are written as blocks finish.
type unitTiming struct {
	// lastInput is when the latest input the model could answer was
	// written: the instruction, then each tool result.
	lastInput time.Time
	// open holds each call awaiting its result, by call id.
	open map[string]openCall
	// requests maps a request id to its span's index in spans.
	requests map[string]int
	spans    []Span
	matched  int
	timed    int
	// lastTurn and lastEnd decide completion: a turn end at or after the
	// unit's last turn.
	lastTurn, lastEnd time.Time
	turnMs            int64
	turnEnds          int
	ttftMs            int64
	ttftRecorded      bool
}

type openCall struct {
	at   time.Time
	kind SpanKind
}

func newUnitTiming(promptAt time.Time) *unitTiming {
	return &unitTiming{lastInput: promptAt, open: map[string]openCall{}, requests: map[string]int{}}
}

// turn notes one assistant entry.
func (t *unitTiming) turn(r Record) {
	previous := t.lastTurn
	if r.At.After(t.lastTurn) {
		t.lastTurn = r.At
	}
	if r.RequestID == "" {
		return
	}
	if i, ok := t.requests[r.RequestID]; ok {
		if i >= 0 && r.At.After(t.spans[i].End) {
			t.spans[i].End = r.At
		}
		return
	}
	// A new request is sent after its input was written and after the
	// previous response finished, whichever came later.
	start := t.lastInput
	if previous.After(start) {
		start = previous
	}
	// A request with no input before it in this unit has no start.
	if start.IsZero() || r.At.Before(start) {
		t.requests[r.RequestID] = -1
		return
	}
	t.requests[r.RequestID] = len(t.spans)
	t.spans = append(t.spans, Span{Kind: SpanModel, Start: start, End: r.At})
	t.timed++
}

// call notes a tool call, which opens a span when it carries an id.
func (t *unitTiming) call(r Record) {
	if r.CallID == "" {
		return
	}
	kind := SpanTool
	if operatorWaitTools[strings.ToLower(r.ToolName)] {
		kind = SpanOperatorWait
	}
	t.open[r.CallID] = openCall{at: r.At, kind: kind}
}

// result closes the span its call opened.
func (t *unitTiming) result(r Record) {
	if r.At.After(t.lastInput) {
		t.lastInput = r.At
	}
	c, ok := t.open[r.CallID]
	if !ok {
		return
	}
	delete(t.open, r.CallID)
	t.matched++
	if r.At.Before(c.at) {
		return
	}
	t.spans = append(t.spans, Span{Kind: c.kind, Start: c.at, End: r.At})
}

// turnEnd notes the harness recording that a turn finished.
func (t *unitTiming) turnEnd(r Record) {
	if r.At.After(t.lastEnd) {
		t.lastEnd = r.At
	}
	if r.DurationMs > 0 {
		t.turnMs += r.DurationMs
		t.turnEnds++
	}
	if r.TTFTRecorded && !t.ttftRecorded {
		t.ttftMs, t.ttftRecorded = r.TTFTMs, true
	}
}

// finish writes what was collected onto the unit.
func (t *unitTiming) finish(u *Unit) {
	if t == nil {
		return
	}
	u.Spans = t.spans
	u.MatchedToolCalls = t.matched
	u.ModelRequests = len(t.requests)
	u.TimedModelRequests = t.timed
	u.Completed = u.Turns > 0 && !t.lastEnd.IsZero() && !t.lastEnd.Before(t.lastTurn)
	u.HarnessTurnMs, u.HarnessTurns = t.turnMs, t.turnEnds
	u.TTFTMs, u.TTFTRecorded = t.ttftMs, t.ttftRecorded
}
