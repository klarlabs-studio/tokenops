package moveverdict

import (
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Settle is how long after a subagent returns its outcome stays open:
// long enough for the parent to re-run the work, rework what it wrote or
// the operator to stop the turn. A call is judged only once it has
// settled, so a run is never scored on half its evidence.
const Settle = 30 * time.Minute

// MaxRun is how long a subagent call without a result is waited on.
// Past it the session ended or crashed mid-call, and the call is left
// unjudged rather than read again forever.
const MaxRun = 24 * time.Hour

// rerunSimilarity is how alike two subagent briefs must be (word
// Jaccard) to count as the same work launched again. Agent tool
// descriptions are three to five words, so 0.6 asks that most of them
// match while allowing "again" or a reworded noun.
const rerunSimilarity = 0.6

// MaxSessionUnits caps the instructions a session contributes. Without a
// cap long sessions dominate, and late instructions in a long session
// behave differently from early ones whatever the model (the session-
// position confound); both sides are capped alike.
const MaxSessionUnits = 20

// Call is one subagent launch in a parent session, as the transcript
// adapter read it.
type Call struct {
	// Ref is the Agent call's tool_use_id.
	Ref string
	At  time.Time
	// Kind is the task kind its brief classifies as, "" when it does
	// not; Model the family it ran on.
	Kind  string
	Model string
	// Words is the brief's description, lower-cased, for recognising a
	// re-run. Held in memory only, never stored.
	Words string
	// ResultAt and HasResult are when its result arrived. Excluded marks
	// a call that never ran as itself (declined at a prompt, or stopped
	// by the harness), which says nothing about quality.
	ResultAt  time.Time
	HasResult bool
	Excluded  bool
	// Failed marks a result recorded as an error.
	Failed bool
	// Mentions are the file paths its brief and report name.
	Mentions []string
}

// Prompt is an instruction the operator typed.
type Prompt struct {
	At      time.Time
	Rejects bool
}

// Edit is a file edit the parent session made.
type Edit struct {
	At   time.Time
	File string
}

// Timeline is what one parent session's transcript says about its
// subagents and what happened around them.
type Timeline struct {
	Session, Scope string
	Calls          []Call
	Prompts        []Prompt
	Interrupts     []time.Time
	Edits          []Edit
	// ModelSwitch is when the session first ran on a model family other
	// than the one it started on (a /model switch), zero when it never
	// did.
	ModelSwitch time.Time
}

// Move is a move the coach made (or the operator approved) on one call,
// as the follow-through ledger has it, with model families.
type Move struct {
	ID, Kind, From, To string
	// Held marks a move the coach held back at random: the call ran on
	// From, and is the comparison for the move to To.
	Held bool
}

// ObserveCalls turns a session's settled subagent calls into
// observations, marking the ones in moves (by tool_use_id). pending
// reports calls not yet settled, so the session is read again later.
func ObserveCalls(tl Timeline, moves map[string]Move, now time.Time) ([]Observation, bool) {
	var out []Observation
	pending := false
	for i, c := range tl.Calls {
		m, moved := moves[c.Ref]
		if c.Excluded || (!moved && (c.Kind == "" || c.Model == "")) {
			continue
		}
		if !c.HasResult {
			if now.Sub(c.At) < MaxRun {
				pending = true
			}
			continue
		}
		if now.Sub(c.ResultAt) < Settle {
			pending = true
			continue
		}
		o := Observation{
			Ref: c.Ref, Session: tl.Session, Scope: tl.Scope, Source: SourceClaudeTranscript,
			At: c.At.UTC(), Kind: c.Kind, Model: c.Model, Trials: 1,
		}
		switch {
		case moved && m.Held:
			o.Held, o.From, o.HeldFor = true, m.From, m.To
			if m.Kind != "" {
				o.Kind = m.Kind
			}
		case moved:
			o.Moved, o.MoveID, o.From = true, m.ID, m.From
			if m.Kind != "" {
				o.Kind = m.Kind
			}
			if m.To != "" {
				o.Model = m.To
			}
		}
		o.Signals = callSignals(tl, i)
		if o.Signals != (Signals{}) {
			o.Bad = 1
		}
		out = append(out, o)
	}
	return out, pending
}

func callSignals(tl Timeline, i int) Signals {
	c := tl.Calls[i]
	var s Signals
	if c.Failed {
		s.Failed = 1
	}
	unitEnd, rejects := nextPrompt(tl.Prompts, c.At)
	inUnit := func(t time.Time, after time.Time) bool {
		return t.After(after) && (unitEnd.IsZero() || t.Before(unitEnd))
	}
	for _, it := range tl.Interrupts {
		if inUnit(it, c.At) {
			s.Interrupted = 1
			break
		}
	}
	if rejects {
		s.Rejected = 1
	}
	for _, e := range tl.Edits {
		if inUnit(e.At, c.ResultAt) && mentioned(e.File, c.Mentions) {
			s.Reworked = 1
			break
		}
	}
	for j, d := range tl.Calls {
		if j == i || !d.At.After(c.At) || d.Kind != c.Kind || d.At.Sub(c.ResultAt) > Settle {
			continue
		}
		if similarity(c.Words, d.Words) >= rerunSimilarity {
			s.Rerun = 1
			break
		}
	}
	return s
}

// nextPrompt is the first instruction after t, which closes t's unit,
// and whether it rejected what came before.
func nextPrompt(prompts []Prompt, t time.Time) (time.Time, bool) {
	var best Prompt
	for _, p := range prompts {
		if p.At.After(t) && (best.At.IsZero() || p.At.Before(best.At)) {
			best = p
		}
	}
	return best.At, best.Rejects
}

// mentioned reports whether file is one of the paths named: the same
// path, a path ending in it, or, for a bare name, the same base name.
func mentioned(file string, mentions []string) bool {
	if file == "" {
		return false
	}
	for _, m := range mentions {
		m = strings.TrimPrefix(strings.TrimSpace(m), "./")
		switch {
		case m == "":
		case file == m, strings.HasSuffix(file, "/"+strings.TrimPrefix(m, "/")):
			return true
		case !strings.Contains(m, "/") && path.Base(file) == m:
			return true
		}
	}
	return false
}

// stopWords carry no sense of what the work is.
var stopWords = map[string]bool{"the": true, "and": true, "for": true, "with": true, "into": true, "from": true, "this": true, "that": true}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(w) >= 3 && !stopWords[w] {
			out[w] = true
		}
	}
	return out
}

// similarity is the word Jaccard of two briefs; 0 when either is empty.
func similarity(a, b string) float64 {
	wa, wb := words(a), words(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	inter := 0
	for w := range wa {
		if wb[w] {
			inter++
		}
	}
	return float64(inter) / float64(len(wa)+len(wb)-inter)
}

// UnitOutcome is one instruction unit's agent DX signals.
type UnitOutcome struct {
	Reworked, Interrupted, Rejected bool
}

// ObserveSession is one session's evidence for the default-model lever:
// its first MaxSessionUnits instructions on model, marked moved when the
// session started under the coach's lowered default (lever).
func ObserveSession(session, scope string, start time.Time, model string, units []UnitOutcome, lever *Move) Observation {
	o := Observation{
		Ref: session, Session: session, Scope: scope, Source: SourceClaudeTranscript,
		At: start.UTC(), Kind: KindDefaultModel, Model: model,
	}
	if lever != nil {
		o.Moved, o.MoveID, o.From = true, lever.ID, lever.From
	}
	if len(units) > MaxSessionUnits {
		units = units[:MaxSessionUnits]
	}
	for _, u := range units {
		o.Trials++
		if u.Reworked {
			o.Signals.Reworked++
		}
		if u.Interrupted {
			o.Signals.Interrupted++
		}
		if u.Rejected {
			o.Signals.Rejected++
		}
		if u.Reworked || u.Interrupted || u.Rejected {
			o.Bad++
		}
	}
	return o
}

// UnitOutcomes reads a session's instruction units from its timeline,
// with the agent DX signals as `tokenops dx` defines them: an edit that
// returns to a file the unit had moved on from is rework (a run of edits
// to one file is one episode), an interrupt inside the unit, and an
// instruction that rejects the unit before it. Units starting at or after
// until (the session's first model switch, zero for none) are left out:
// they ran on another model.
func UnitOutcomes(tl Timeline, until time.Time) []UnitOutcome {
	prompts := append([]Prompt(nil), tl.Prompts...)
	sort.Slice(prompts, func(i, j int) bool { return prompts[i].At.Before(prompts[j].At) })
	var out []UnitOutcome
	for i, p := range prompts {
		if !until.IsZero() && !p.At.Before(until) {
			break
		}
		var end time.Time
		if i+1 < len(prompts) {
			end = prompts[i+1].At
		}
		in := func(t time.Time) bool { return !t.Before(p.At) && (end.IsZero() || t.Before(end)) }
		u := UnitOutcome{Rejected: i+1 < len(prompts) && prompts[i+1].Rejects}
		for _, it := range tl.Interrupts {
			if in(it) {
				u.Interrupted = true
				break
			}
		}
		seen := map[string]bool{}
		last := ""
		for _, e := range tl.Edits {
			if !in(e.At) || e.File == last {
				continue
			}
			if seen[e.File] {
				u.Reworked = true
			}
			seen[e.File], last = true, e.File
		}
		out = append(out, u)
	}
	return out
}
