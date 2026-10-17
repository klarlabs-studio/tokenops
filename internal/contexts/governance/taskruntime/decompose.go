package taskruntime

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/governance/story"
	"go.klarlabs.de/tokenops/internal/contexts/measurement"
)

// precedence orders the categories an instant can belong to; lower wins.
var precedence = map[Category]int{
	CategoryApprovalWait:  0,
	CategoryToolExecution: 1,
	CategoryModelRequest:  2,
	CategoryIdle:          3,
}

type labelled struct {
	interval
	cat Category
}

// decompose assigns every instant of [t.Start, end] to exactly one
// category, so the categories add up to the observed interval and no
// overlap is counted twice.
func decompose(t story.Task, harness string, end time.Time, opts Options) []CategoryTime {
	spans, dropped := recordedIntervals(t, opts)
	seconds := sweep(spans, t.Start, end)
	observed := end.Sub(t.Start).Seconds()

	var requests, timed int
	for _, u := range t.Units {
		requests += u.ModelRequests
		timed += u.TimedModelRequests
	}
	calls, matched := 0, 0
	for _, u := range t.Units {
		calls += u.ToolCalls
		matched += u.MatchedToolCalls
	}

	out := make([]CategoryTime, 0, len(Categories))
	var unknown []string
	for _, c := range Categories {
		ct := describe(c, harness, requests, timed-dropped, calls, matched, dropped)
		if ct.Quality != measurement.QualityUnknown {
			s := seconds[c]
			share := 0.0
			if observed > 0 {
				share = math.Round(s/observed*1e4) / 1e4
			}
			// Transcript timestamps carry milliseconds; more digits are
			// float noise.
			s = math.Round(s*1e3) / 1e3
			ct.Seconds, ct.Share = &s, &share
		} else {
			unknown = append(unknown, string(c))
		}
		out = append(out, ct)
	}
	if len(unknown) > 0 {
		for i := range out {
			if out[i].Category == CategoryUnattributed {
				out[i].Caveat = "includes any time spent on " + strings.Join(unknown, ", ") +
					", which this harness does not record"
			}
		}
	}
	return out
}

// recordedIntervals is every interval the task's transcript records,
// labelled, plus how many model spans were too long to attribute.
func recordedIntervals(t story.Task, opts Options) ([]labelled, int) {
	var out []labelled
	dropped := 0
	var prevEnd time.Time
	for i, u := range t.Units {
		if i > 0 && !prevEnd.IsZero() && u.Start.After(prevEnd) {
			out = append(out, labelled{interval{prevEnd, u.Start}, CategoryIdle})
		}
		unitEnd := u.LastActivity
		for _, s := range u.Spans {
			if s.End.After(unitEnd) {
				unitEnd = s.End
			}
			switch s.Kind {
			case agentdx.SpanOperatorWait:
				out = append(out, labelled{interval{s.Start, s.End}, CategoryApprovalWait})
			case agentdx.SpanTool:
				out = append(out, labelled{interval{s.Start, s.End}, CategoryToolExecution})
			case agentdx.SpanModel:
				if s.End.Sub(s.Start) > opts.maxModelSpan() {
					dropped++
					continue
				}
				out = append(out, labelled{interval{s.Start, s.End}, CategoryModelRequest})
			}
		}
		if unitEnd.After(prevEnd) {
			prevEnd = unitEnd
		}
	}
	return out, dropped
}

// sweep walks the intervals' edges once, giving each stretch between two
// edges to the highest-precedence category covering it, or to
// unattributed when none does.
func sweep(spans []labelled, from, to time.Time) map[Category]float64 {
	type edge struct {
		at    time.Time
		cat   Category
		delta int
	}
	edges := []edge{{at: from}, {at: to}}
	for _, s := range spans {
		start, end := s.start, s.end
		if start.Before(from) {
			start = from
		}
		if end.After(to) {
			end = to
		}
		if !end.After(start) {
			continue
		}
		edges = append(edges, edge{start, s.cat, 1}, edge{end, s.cat, -1})
	}
	sort.SliceStable(edges, func(i, j int) bool { return edges[i].at.Before(edges[j].at) })
	active := map[Category]int{}
	out := map[Category]float64{}
	prev := from
	for _, e := range edges {
		if e.at.After(prev) {
			out[top(active)] += e.at.Sub(prev).Seconds()
			prev = e.at
		}
		if e.delta != 0 {
			active[e.cat] += e.delta
		}
	}
	return out
}

func top(active map[Category]int) Category {
	best, rank := CategoryUnattributed, len(precedence)
	for c, n := range active {
		if n > 0 && precedence[c] < rank {
			best, rank = c, precedence[c]
		}
	}
	return best
}

// recordsRequests are the harnesses whose transcripts group entries by
// model request; recordsToolResults those that record a tool's result as
// its own entry.
var (
	recordsRequests    = map[string]bool{string(agentdx.SourceClaudeCode): true}
	recordsToolResults = map[string]bool{string(agentdx.SourceClaudeCode): true, string(agentdx.SourceCodex): true}
)

// describe says where a category's figure comes from for a harness, or
// why there is none.
func describe(c Category, harness string, requests, timed, calls, matched, dropped int) CategoryTime {
	ct := CategoryTime{Category: c}
	unknown := func(why string) CategoryTime {
		ct.Quality, ct.Origin = measurement.QualityUnknown, "unknown: "+why
		return ct
	}
	switch c {
	case CategoryModelRequest:
		switch {
		case !recordsRequests[harness]:
			return unknown(harness + " transcripts record no model-request boundaries")
		case requests == 0:
			return unknown("no request ids were recorded for this task's turns")
		}
		ct.Quality = measurement.QualityEstimated
		ct.Origin = "transcript: the input written before a request to the last entry that request wrote"
		ct.Caveat = "includes queueing and streaming; it is not time to first token"
		if dropped > 0 {
			ct.Caveat += fmt.Sprintf("; %d span(s) longer than the plausibility bound left unattributed", dropped)
		}
		ct.Timed, ct.Seen = timed, requests
		ct.Complete = timed == requests
	case CategoryToolExecution:
		switch {
		case !recordsToolResults[harness]:
			return unknown(harness + " transcripts record tool calls without their results")
		case calls > 0 && matched == 0:
			return unknown("no tool call in this task has a recorded result")
		}
		ct.Quality = measurement.QualityMeasured
		ct.Origin = "transcript: each tool call's entry to its result's entry, paired by call id; overlap counted once"
		ct.Timed, ct.Seen = matched, calls
		ct.Complete = matched == calls
		if harness == string(agentdx.SourceClaudeCode) {
			ct.Caveat = "a permission prompt is not recorded; any wait for one sits inside its tool's span"
		}
	case CategoryApprovalWait:
		if harness != string(agentdx.SourceClaudeCode) {
			return unknown(harness + " transcripts do not record approval requests")
		}
		ct.Quality = measurement.QualityMeasured
		ct.Origin = "transcript: spans of tools that ask the operator (AskUserQuestion, ExitPlanMode)"
		ct.Caveat = "a lower bound: permission prompts are not recorded, and their wait sits in tool_execution"
		ct.Complete = false
	case CategoryIdle:
		ct.Quality = measurement.QualityMeasured
		ct.Origin = "transcript: from the agent's last activity on one instruction to the next instruction"
		ct.Complete = true
	case CategoryUnattributed:
		ct.Quality = measurement.QualityDerived
		ct.Origin = "the observed interval less every recorded interval"
		ct.Complete = true
	}
	return ct
}
