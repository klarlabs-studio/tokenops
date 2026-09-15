package agentdx

import "time"

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
	// reads as $94k when cache-aware pricing says $10k. Use it for
	// comparing units against each other, never as a spend number.
	Tokens int64
	// PeakContext is the largest single turn's input: how big the working
	// context actually got. This is the honest "how heavy was this"
	// number, because it counts the context once.
	PeakContext int64

	// Edits and ReworkEdits count file edits and those that revisited a
	// file this unit had already edited.
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
}

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
func Units(records []Record) []Unit {
	if len(records) == 0 {
		return nil
	}
	sorted := sortedByTime(records)

	var (
		out          []Unit
		cur          Unit
		inUnit       bool
		edited       map[string]bool
		seenTool     map[string]bool
		seenFile     map[string]bool
		lastInputTok int64
	)

	closeUnit := func() {
		if inUnit {
			out = append(out, cur)
		}
		inUnit = false
	}

	for _, r := range sorted {
		switch r.Kind {
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
			}
			edited = map[string]bool{}
			seenTool = map[string]bool{}
			seenFile = map[string]bool{}
			lastInputTok = 0
			inUnit = true
		case KindAssistantTurn:
			if !inUnit {
				continue
			}
			cur.Turns++
			cur.End = r.At
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
			if r.ToolName != "" && !seenTool[r.ToolName] {
				seenTool[r.ToolName] = true
				cur.Tools = append(cur.Tools, r.ToolName)
			}
			if isEscalation(r.ToolName) {
				cur.Escalated = true
			}
			if isEdit(r.ToolName) && r.FilePath != "" {
				cur.Edits++
				if edited[r.FilePath] {
					cur.ReworkEdits++
					cur.Reworked = true
				}
				edited[r.FilePath] = true
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
