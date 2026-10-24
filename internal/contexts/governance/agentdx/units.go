package agentdx

import (
	"sort"
	"time"
)

// Unit is one operator instruction and everything the agent did before
// the next one. It is the atom both the metrics and the narrative are
// built from: Compute folds units into rates and percentiles, while the
// story layer reads them one at a time and says what happened.
//
// Prompt carries the instruction text and is populated only when the
// extraction was asked for it (ExtractOptions.WithPromptText). It exists
// in memory for the length of a scan and is never written to the event
// store — the same rule the coaching surfaces have always followed.
type Unit struct {
	SessionID string
	Provider  string
	// Model and Effort are what served most of this unit's turns, empty
	// when the client did not record them.
	Model  string
	Effort string

	// Prompt is the operator's instruction. Empty unless the extraction
	// was asked to carry text.
	Prompt string

	// Start is when the instruction was typed; End is the agent's last
	// turn answering it, zero when it never answered.
	Start time.Time
	End   time.Time

	Turns     int
	ToolCalls int
	// Tokens is the context carried across this unit's turns, summed.
	//
	// It is NOT what the unit cost. Every turn re-sends the accumulated
	// context, so this double-counts by design — it is the figure that
	// reads about nine times what cache-aware pricing says. Use it for
	// comparing units against each other, never as a spend number.
	Tokens int64
	// PeakContext is the largest single turn's input: how big the working
	// context actually got. This is the honest "how heavy was this"
	// number, because it counts the context once.
	PeakContext int64

	// Edits counts file edits. ReworkEdits counts the subset that RETURNED
	// to a file after the unit had moved on to another one — a run of
	// consecutive edits to one file is a single editing episode, not
	// rework, because making a change in several parts is how editing
	// works.
	Edits       int
	ReworkEdits int

	// Growths are the context increases between consecutive turns, the
	// samples behind MedianContextGrowthTokens.
	Growths []float64

	// Tools and Files are the distinct names touched, in order of first
	// use — what the agent reached for, in the order it reached.
	Tools []string
	Files []string

	Reworked    bool
	Interrupted bool
	Escalated   bool
	// Rejected is set when the *next* instruction rejected this one's
	// output. It is the only quality verdict a transcript carries: the
	// operator read the reply and said it was wrong.
	Rejected bool
	// PromptRejects is set when *this* unit's own instruction rejected
	// what came before it. Rejected looks forward, this looks back, and
	// the difference matters: an instruction that rejects is by
	// definition about the work already in flight, so it can never be
	// the start of something new.
	PromptRejects bool

	// Harness is the client the unit was read from; Role is what the
	// instruction was (RolePrimary, RoleAutoReview, RoleSubagent).
	Harness string
	Role    string

	// LastActivity is the unit's last recorded entry of any kind: its
	// last turn, or a tool result that arrived after it.
	LastActivity time.Time
	// Spans are the timed intervals the transcript records for the unit:
	// tool calls with their results, operator questions, and model
	// requests where the client groups entries by request.
	Spans []Span
	// MatchedToolCalls counts tool calls whose result was recorded;
	// ToolCalls - MatchedToolCalls had no recorded end.
	MatchedToolCalls int
	// ModelRequests counts distinct request ids among the unit's turns;
	// TimedModelRequests the subset with a recorded input before them.
	ModelRequests      int
	TimedModelRequests int
	// Completed is set when the harness recorded the end of the unit's
	// last turn (KindTurnEnd at or after it): the only explicit
	// completion evidence a transcript carries. It says the turn ended,
	// not that the work was right.
	Completed bool
	// HarnessTurnMs sums the turn durations the harness recorded;
	// HarnessTurns counts the turn ends that carried one.
	HarnessTurnMs int64
	HarnessTurns  int
	// TTFTMs is the first turn's time to first token, as the harness
	// recorded it; meaningful only when TTFTRecorded.
	TTFTMs       int64
	TTFTRecorded bool
}

// SpanKind names what a recorded interval was spent on.
type SpanKind string

// Span kinds.
const (
	// SpanTool is a tool call, from the call entry to its result entry.
	SpanTool SpanKind = "tool"
	// SpanOperatorWait is a tool that asks the operator something and
	// waits for the answer (AskUserQuestion, ExitPlanMode).
	SpanOperatorWait SpanKind = "operator_wait"
	// SpanModel is a model request, from the input written before it to
	// the last entry the request wrote. It includes queueing and
	// streaming; it is not time to first token.
	SpanModel SpanKind = "model"
)

// Span is one recorded interval.
type Span struct {
	Kind  SpanKind
	Start time.Time
	End   time.Time
}

// operatorWaitTools are the tools whose call blocks on the operator's
// answer, so their span is time spent waiting for a person rather than
// for a machine.
var operatorWaitTools = map[string]bool{"askuserquestion": true, "exitplanmode": true}

// Duration is how long the operator waited, or zero when the unit
// produced no turns — a prompt typed at the end of a session would
// otherwise measure how long the window stayed open.
func (u Unit) Duration() time.Duration {
	if u.Turns == 0 || u.End.IsZero() || !u.End.After(u.Start) {
		return 0
	}
	return u.End.Sub(u.Start)
}

// FirstTry reports whether the agent answered without reworking a file,
// being interrupted, or delegating to a subagent. The closest single
// signal for "it just worked".
func (u Unit) FirstTry() bool {
	return u.Turns > 0 && !u.Reworked && !u.Interrupted && !u.Escalated
}

// Units groups records into instruction units, in chronological order.
//
// Entries before the first prompt belong to no unit and are dropped
// rather than attributed to an invented one: a transcript routinely
// opens mid-conversation, and charging that work to a prompt nobody
// typed would put the cost of a resumed session on its first
// instruction.
//
// Units are built per session. Concurrent sessions interleave in
// wall-clock time, and walking them in one time order handed one
// session's turns and tool calls to whichever session's instruction was
// typed last.
func Units(records []Record) []Unit {
	if len(records) == 0 {
		return nil
	}
	var out []Unit
	for _, recs := range bySession(records) {
		out = append(out, sessionUnits(recs)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// bySession partitions records per session, in the order sessions first
// appear.
func bySession(records []Record) [][]Record {
	idx := map[string]int{}
	var out [][]Record
	for _, r := range records {
		i, ok := idx[r.SessionID]
		if !ok {
			i = len(out)
			idx[r.SessionID] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], r)
	}
	return out
}

// sessionUnits groups one session's records into units.
func sessionUnits(records []Record) []Unit {
	sorted := sortedByTime(records)

	var (
		// timing tracks the current unit's open spans.
		timing *unitTiming
		out    []Unit
		cur    Unit
		inUnit bool
		edited map[string]bool
		// lastEdited is the file the current editing episode belongs to.
		lastEdited   string
		seenTool     map[string]bool
		seenFile     map[string]bool
		lastInputTok int64
		// Turns per model and per effort, so the unit takes the one
		// that served most of it.
		models, efforts map[string]int
	)

	closeUnit := func() {
		if inUnit {
			cur.Model, cur.Effort = mostCommon(models), mostCommon(efforts)
			cur.Role = RoleOf(cur.Role, cur.Model)
			timing.finish(&cur)
			out = append(out, cur)
		}
		inUnit = false
	}

	for _, r := range sorted {
		if inUnit && r.Kind != KindPrompt && r.At.After(cur.LastActivity) {
			cur.LastActivity = r.At
		}
		switch r.Kind {
		case KindToolResult:
			if inUnit {
				timing.result(r)
			}
		case KindTurnEnd:
			if inUnit {
				timing.turnEnd(r)
			}
		case KindPrompt:
			// A prompt that rejects what preceded it is a verdict on the
			// unit that just closed, not on itself.
			if r.Rejects && inUnit {
				cur.Rejected = true
			}
			closeUnit()
			cur = Unit{
				SessionID:     r.SessionID,
				Prompt:        r.Text,
				Start:         r.At,
				Provider:      r.Provider,
				PromptRejects: r.Rejects,
				Harness:       string(r.Harness),
				Role:          r.Role,
				LastActivity:  r.At,
			}
			timing = newUnitTiming(r.At)
			edited = map[string]bool{}
			lastEdited = ""
			seenTool = map[string]bool{}
			seenFile = map[string]bool{}
			lastInputTok = 0
			models, efforts = map[string]int{}, map[string]int{}
			inUnit = true
		case KindAssistantTurn:
			if !inUnit {
				continue
			}
			cur.Turns++
			cur.End = r.At
			timing.turn(r)
			if r.Model != "" {
				models[r.Model]++
			}
			if r.Effort != "" {
				efforts[r.Effort]++
			}
			cur.Tokens += r.InputTokens
			if r.InputTokens > cur.PeakContext {
				cur.PeakContext = r.InputTokens
			}
			if cur.Provider == "" {
				cur.Provider = r.Provider
			}
			if r.InputTokens > 0 {
				if lastInputTok > 0 && r.InputTokens > lastInputTok {
					cur.Growths = append(cur.Growths, float64(r.InputTokens-lastInputTok))
				}
				lastInputTok = r.InputTokens
			}
		case KindToolUse:
			if !inUnit {
				continue
			}
			cur.ToolCalls++
			timing.call(r)
			if r.ToolName != "" && !seenTool[r.ToolName] {
				seenTool[r.ToolName] = true
				cur.Tools = append(cur.Tools, r.ToolName)
			}
			if isEscalation(r.ToolName) {
				cur.Escalated = true
			}
			if isEdit(r.ToolName) && r.FilePath != "" {
				cur.Edits++
				// Rework means coming BACK to a file, not editing it more
				// than once.
				//
				// Keying on the path alone counted every multi-part change
				// as waste: two Edit calls to different sections of one
				// file — the ordinary way to make a change — scored as
				// "revisited a file this unit had already edited". Since
				// this metric feeds the overall grade, and the grade is the
				// worst of its parts, ordinary editing could take the whole
				// headline down to an F.
				//
				// So a run of consecutive edits to one file is a single
				// editing episode, and only a return after working
				// elsewhere counts. Reads and other tool calls in between
				// do not end the episode: looking at the file you are
				// editing is part of editing it.
				//
				// This under-counts one real case — editing a file, running
				// a test that fails, and fixing the same file — because the
				// records do not reliably say whether the intervening call
				// failed. That is the conservative direction for a number
				// that grades the operator's work.
				if edited[r.FilePath] && lastEdited != r.FilePath {
					cur.ReworkEdits++
					cur.Reworked = true
				}
				edited[r.FilePath] = true
				lastEdited = r.FilePath
				if !seenFile[r.FilePath] {
					seenFile[r.FilePath] = true
					cur.Files = append(cur.Files, r.FilePath)
				}
			}
		case KindInterrupt:
			if inUnit {
				cur.Interrupted = true
			}
		}
	}
	closeUnit()
	return out
}

// mostCommon is the key with the highest count, ties broken by name so
// the answer does not depend on map order; "" for an empty map.
func mostCommon(counts map[string]int) string {
	best, bestN := "", 0
	for k, n := range counts {
		if n > bestN || (n == bestN && k < best) {
			best, bestN = k, n
		}
	}
	return best
}
