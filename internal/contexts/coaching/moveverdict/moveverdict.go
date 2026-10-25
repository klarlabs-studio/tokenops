// Package moveverdict judges whether the coach's autonomous model moves
// held quality, and retracts the ones that did not (ADR 0004: learning
// is outcome-based and retractable).
//
// The coach moves work to a cheaper model on its own: a subagent a tier
// down (route guard), or the model new sessions start on (the default-
// model lever). This package keeps the three things AGENTS.md says stay
// distinct apart:
//
//   - Evidence: an Observation is one run, moved or not, with what went
//     wrong in it, where it was read (Source), when and in what project
//     (Scope). It is recorded whether or not anything was moved, so moved
//     runs have like runs to be compared with.
//   - Belief: per move kind (task kind, from-model → to-model), "this move
//     holds quality", judged from moved runs against comparable unmoved
//     ones, with the sample, the rates and how confident the drop is.
//   - Decision: a Retraction, made when the belief says quality dropped,
//     with the alternatives weighed and the reason. It ends only on new
//     evidence or on the operator's word, never because old evidence aged
//     out.
//
// It is pure: callers read transcripts and persist observations and
// retractions through their own adapters.
package moveverdict

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// SourceClaudeTranscript is the provenance of an observation read from a
// Claude Code session transcript.
const SourceClaudeTranscript = "claude-code transcript"

// KindDefaultModel names the default-model lever's move: whole sessions
// started on a lower model, rather than one piece of subagent work.
const KindDefaultModel = "default_model"

// Lookback bounds the evidence judged. Older runs say little about the
// models and the work as they are now, and an unbounded history turns
// every rate into a measure of how long TokenOps has been watching.
const Lookback = 28 * 24 * time.Hour

// Signals counts what went wrong in a run. For a subagent call each is 0
// or 1; for a session each counts the instruction units it marks.
type Signals struct {
	// Failed: the subagent's call came back as an error.
	Failed int `json:"failed,omitempty"`
	// Rerun: the parent launched the same work again soon after.
	Rerun int `json:"rerun,omitempty"`
	// Reworked: the parent edited, after the subagent returned, a file
	// the subagent's brief or report named; for a session, a unit that
	// returned to a file it had moved on from (agent DX rework).
	Reworked int `json:"reworked,omitempty"`
	// Interrupted: the operator stopped the turn the run was part of.
	Interrupted int `json:"interrupted,omitempty"`
	// Rejected: the operator's next instruction rejected the result.
	Rejected int `json:"rejected,omitempty"`
}

func (s Signals) add(o Signals) Signals {
	return Signals{
		Failed: s.Failed + o.Failed, Rerun: s.Rerun + o.Rerun, Reworked: s.Reworked + o.Reworked,
		Interrupted: s.Interrupted + o.Interrupted, Rejected: s.Rejected + o.Rejected,
	}
}

// Observation is the evidence from one run.
type Observation struct {
	// Ref names the run: the subagent call's tool_use_id, or the session
	// id for a session.
	Ref     string `json:"ref"`
	Session string `json:"session"`
	// Scope is the project the run belongs to. Runs are only compared
	// within one project: the work differs more between projects than
	// between models.
	Scope  string    `json:"scope"`
	Source string    `json:"source"`
	At     time.Time `json:"at"`
	// Kind is the task kind ("edit", "deep", …) or KindDefaultModel.
	Kind string `json:"kind"`
	// Model is the model family the run actually used ("opus", "sonnet",
	// "haiku").
	Model string `json:"model"`
	// Moved marks a run the coach moved (or the operator approved moving)
	// from From; MoveID is the move's entry in the follow-through ledger.
	Moved  bool   `json:"moved,omitempty"`
	From   string `json:"from,omitempty"`
	MoveID string `json:"move_id,omitempty"`
	// Trials is 1 for a subagent call, the units counted for a session;
	// Bad how many of them had any signal.
	Trials  int     `json:"trials"`
	Bad     int     `json:"bad"`
	Signals Signals `json:"signals"`
}

// Key names a kind of move.
type Key struct {
	Kind string `json:"kind"`
	From string `json:"from"`
	To   string `json:"to"`
}

func (k Key) String() string { return fmt.Sprintf("%s: %s → %s", k.Kind, k.From, k.To) }

// Verdict is what the evidence says about a move kind.
type Verdict string

// The verdicts.
const (
	// VerdictUnjudged: not enough comparable evidence yet.
	VerdictUnjudged Verdict = "unjudged"
	// VerdictHolds: enough evidence, and no drop beyond the threshold.
	VerdictHolds Verdict = "holds"
	// VerdictDropped: quality dropped beyond the threshold, confidently.
	VerdictDropped Verdict = "dropped"
)

// Policy is when a belief may be judged and when a drop counts.
type Policy struct {
	// MinTreated and MinBaseline are the trials each side needs;
	// MinTreatedRuns and MinBaselineRuns the distinct runs.
	MinTreated, MinBaseline         int
	MinTreatedRuns, MinBaselineRuns int
	// MinDrop is how far, in share of trials, the moved runs must do
	// worse; MinZ how sure (one-sided two-proportion z) that it is not
	// noise.
	MinDrop float64
	MinZ    float64
	// Near is how far apart in time a comparable run may be.
	Near time.Duration
}

// SubagentPolicy judges subagent moves. Eight runs a side is the
// smallest sample on which a fifteen-point gap can clear z 1.645 at all
// (6/8 against 1/8 is z 2.5; 3/8 against 1/8 is 1.15 and holds), so a
// handful of unlucky calls cannot retract a move, and a real drop is
// caught within a few days of normal use.
var SubagentPolicy = Policy{
	MinTreated: 8, MinBaseline: 8, MinTreatedRuns: 8, MinBaselineRuns: 8,
	MinDrop: 0.15, MinZ: 1.645, Near: 7 * 24 * time.Hour,
}

// SessionPolicy judges the default-model lever. Its trials are a
// session's instruction units, which are not independent of each other,
// so it also needs four sessions a side: one bad afternoon must not read
// as a bad model.
var SessionPolicy = Policy{
	MinTreated: 20, MinBaseline: 20, MinTreatedRuns: 4, MinBaselineRuns: 4,
	MinDrop: 0.15, MinZ: 1.645, Near: 7 * 24 * time.Hour,
}

// PolicyFor is the policy that judges k.
func PolicyFor(k Key) Policy {
	if k.Kind == KindDefaultModel {
		return SessionPolicy
	}
	return SubagentPolicy
}

// Belief is what the evidence says about one kind of move.
type Belief struct {
	Key Key `json:"key"`
	// Moves counts the moved runs in the lookback; Judged those with at
	// least one comparable run, and Bad how many of those went wrong.
	Moves  int `json:"moves"`
	Judged int `json:"judged"`
	Bad    int `json:"bad"`
	// Baseline counts the comparable unmoved trials, BaselineBad those
	// that went wrong.
	Baseline    int `json:"baseline"`
	BaselineBad int `json:"baseline_bad"`
	// Rate and BaselineRate are the shares that went wrong.
	Rate         float64 `json:"rate"`
	BaselineRate float64 `json:"baseline_rate"`
	// Z is the one-sided two-proportion z for "moved did worse";
	// Confidence is the matching normal probability.
	Z          float64 `json:"z"`
	Confidence float64 `json:"confidence"`
	// Signals counts what went wrong in the judged moved runs.
	Signals Signals `json:"signals"`
	Verdict Verdict `json:"verdict"`
	Why     string  `json:"why"`
	// Scope says what was compared with what.
	Scope string `json:"scope"`
	// Since is the oldest moved run judged; AfterRetraction marks a
	// belief judged only on runs since its retraction (new evidence).
	Since           time.Time `json:"since,omitzero"`
	AfterRetraction bool      `json:"after_retraction,omitempty"`
}

// Judge forms a belief for every move kind seen in the lookback and every
// kind retracted. A retracted kind is judged only on runs of that kind on
// the cheaper model since the retraction: the coach no longer moves it,
// so new evidence comes from work the agent or the operator put on the
// cheaper model themselves.
func Judge(obs []Observation, retractions []Retraction, now time.Time) []Belief {
	recent := make([]Observation, 0, len(obs))
	for _, o := range obs {
		if now.Sub(o.At) <= Lookback && o.Trials > 0 {
			recent = append(recent, o)
		}
	}
	retracted := map[Key]time.Time{}
	for _, r := range retractions {
		if r.Active() {
			retracted[r.Key] = r.At
		}
	}
	keys := map[Key]bool{}
	for _, o := range recent {
		if o.Moved && o.From != "" && o.Model != "" {
			keys[Key{Kind: o.Kind, From: o.From, To: o.Model}] = true
		}
	}
	for k := range retracted {
		keys[k] = true
	}
	out := make([]Belief, 0, len(keys))
	for k := range keys {
		at, isRetracted := retracted[k]
		out = append(out, judgeKey(k, recent, at, isRetracted))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.String() < out[j].Key.String() })
	return out
}

func judgeKey(k Key, obs []Observation, retractedAt time.Time, retracted bool) Belief {
	p := PolicyFor(k)
	b := Belief{Key: k, AfterRetraction: retracted}
	b.Scope = fmt.Sprintf("%s runs on %s against unmoved %s runs on %s, same project, within %d days of each other",
		kindLabel(k.Kind), k.To, kindLabel(k.Kind), k.From, int(p.Near.Hours()/24))
	baseline := map[string]Observation{}
	judgedRuns := 0
	for _, t := range obs {
		if !treated(t, k, retractedAt, retracted) {
			continue
		}
		b.Moves++
		var cmp []Observation
		for _, u := range obs {
			if comparable(u, t, k, p.Near) {
				cmp = append(cmp, u)
			}
		}
		if len(cmp) == 0 {
			continue
		}
		for _, u := range cmp {
			baseline[u.Ref] = u
		}
		judgedRuns++
		b.Judged += t.Trials
		b.Bad += t.Bad
		b.Signals = b.Signals.add(t.Signals)
		if b.Since.IsZero() || t.At.Before(b.Since) {
			b.Since = t.At
		}
	}
	for _, u := range baseline {
		b.Baseline += u.Trials
		b.BaselineBad += u.Bad
	}
	if b.Judged > 0 {
		b.Rate = float64(b.Bad) / float64(b.Judged)
	}
	if b.Baseline > 0 {
		b.BaselineRate = float64(b.BaselineBad) / float64(b.Baseline)
	}
	b.Z = twoProportionZ(b.Bad, b.Judged, b.BaselineBad, b.Baseline)
	b.Confidence = 0.5 * math.Erfc(-b.Z/math.Sqrt2)
	switch {
	case b.Judged < p.MinTreated || judgedRuns < p.MinTreatedRuns || b.Baseline < p.MinBaseline || len(baseline) < p.MinBaselineRuns:
		b.Verdict = VerdictUnjudged
		b.Why = fmt.Sprintf("%d of %d %s judged against %d comparable %s; a verdict needs at least %d %s with %d comparable %s",
			judgedRuns, b.Moves, plural(b.Moves, runNoun(k)), len(baseline), plural(len(baseline), runNoun(k)),
			p.MinTreatedRuns, plural(p.MinTreatedRuns, runNoun(k)), p.MinBaselineRuns, plural(p.MinBaselineRuns, runNoun(k)))
		if k.Kind == KindDefaultModel {
			b.Why += fmt.Sprintf(", and %d instructions a side", p.MinTreated)
		}
	case b.Rate-b.BaselineRate >= p.MinDrop && b.Z >= p.MinZ:
		b.Verdict = VerdictDropped
		b.Why = fmt.Sprintf("%s went wrong %s of the time on %s against %s on %s (%d points worse, z %.2f, %.0f%% confident); the rule retracts at %d points and z %.3f",
			trialNoun(k), pct(b.Rate), k.To, pct(b.BaselineRate), k.From, points(b.Rate-b.BaselineRate), b.Z, b.Confidence*100,
			points(p.MinDrop), p.MinZ)
	default:
		b.Verdict = VerdictHolds
		b.Why = fmt.Sprintf("%s went wrong %s of the time on %s against %s on %s; no drop of %d points or more at z %.3f",
			trialNoun(k), pct(b.Rate), k.To, pct(b.BaselineRate), k.From, points(p.MinDrop), p.MinZ)
	}
	return b
}

// treated reports whether o is a run of the move k: one the coach moved,
// or, once k is retracted, any run of its kind on the cheaper model
// since the retraction.
func treated(o Observation, k Key, retractedAt time.Time, retracted bool) bool {
	if o.Kind != k.Kind || o.Model != k.To {
		return false
	}
	if retracted {
		return o.At.After(retractedAt)
	}
	return o.Moved && o.From == k.From
}

// comparable reports whether u is a like-for-like unmoved run for the
// moved run t: same task kind, same project, on the model t was moved
// from, near it in time.
func comparable(u, t Observation, k Key, near time.Duration) bool {
	if u.Moved || u.Ref == t.Ref || u.Kind != k.Kind || u.Model != k.From || u.Scope != t.Scope {
		return false
	}
	d := u.At.Sub(t.At)
	if d < 0 {
		d = -d
	}
	return d <= near
}

// twoProportionZ is the pooled z for "a/n is larger than b/m".
func twoProportionZ(a, n, b, m int) float64 {
	if n == 0 || m == 0 {
		return 0
	}
	p := float64(a+b) / float64(n+m)
	se := math.Sqrt(p * (1 - p) * (1/float64(n) + 1/float64(m)))
	if se == 0 {
		return 0
	}
	return (float64(a)/float64(n) - float64(b)/float64(m)) / se
}

// Ended-by values for a retraction.
const (
	EndedByEvidence = "evidence"
	EndedByOperator = "operator"
)

// Alternative is one course the retraction decision weighed.
type Alternative struct {
	Option string `json:"option"`
	Chosen bool   `json:"chosen,omitempty"`
	Why    string `json:"why"`
}

// Retraction is the decision to stop making a kind of move on its own.
type Retraction struct {
	// ID names the decision.
	ID     string    `json:"id"`
	Key    Key       `json:"key"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
	// Evidence is the belief as it stood when the decision was made.
	Evidence     Belief        `json:"evidence"`
	Alternatives []Alternative `json:"alternatives"`
	// Surfaced marks a retraction the operator has been told about.
	Surfaced bool `json:"surfaced,omitempty"`
	// Ended is when the move was reinstated, EndedBy by what
	// (EndedByEvidence, EndedByOperator), EndedWhy why.
	Ended    time.Time `json:"ended,omitzero"`
	EndedBy  string    `json:"ended_by,omitempty"`
	EndedWhy string    `json:"ended_why,omitempty"`
}

// Active reports whether the retraction still holds.
func (r Retraction) Active() bool { return r.Ended.IsZero() }

// Change is one decision Decide makes.
type Change struct {
	Retract   *Retraction
	Reinstate *Retraction
}

// Decide turns beliefs into decisions: retract a move kind whose quality
// dropped, and reinstate a retracted one when evidence gathered since the
// retraction holds. Anything else, including evidence that simply aged
// out, changes nothing.
func Decide(beliefs []Belief, retractions []Retraction, now time.Time, newID func() string) []Change {
	active := map[Key]Retraction{}
	for _, r := range retractions {
		if r.Active() {
			active[r.Key] = r
		}
	}
	var out []Change
	for _, b := range beliefs {
		r, isRetracted := active[b.Key]
		switch {
		case !isRetracted && b.Verdict == VerdictDropped:
			out = append(out, Change{Retract: &Retraction{
				ID: newID(), Key: b.Key, At: now.UTC(), Reason: b.Why, Evidence: b,
				Alternatives: alternatives(b),
			}})
		case isRetracted && b.AfterRetraction && b.Verdict == VerdictHolds:
			r.Ended, r.EndedBy = now.UTC(), EndedByEvidence
			r.EndedWhy = "runs since the retraction hold quality: " + b.Why
			out = append(out, Change{Reinstate: &r})
		}
	}
	return out
}

func alternatives(b Belief) []Alternative {
	what := moveLabel(b.Key)
	return []Alternative{
		{Option: "keep " + what + " on its own", Why: "the moved runs did measurably worse than like runs that were not moved: " + b.Why},
		{Option: "ask before each such move", Why: "the evidence is about the cheaper model's results on this work, not about consent; an approved move would do as badly"},
		{Option: "stop " + what + " on its own and advise instead", Chosen: true, Why: "the move goes back to advice for this kind of work only; every other move stands, and new evidence or the operator can restore it"},
	}
}

// Lines explains each move kind in plain words: moves made, outcomes
// judged, the verdict and why, and any retraction with the evidence it
// rested on and how to undo it.
func Lines(beliefs []Belief, retractions []Retraction) []string {
	byKey := map[Key]Retraction{}
	for _, r := range retractions {
		if r.Active() {
			byKey[r.Key] = r
		}
	}
	var out []string
	for _, b := range beliefs {
		r, retracted := byKey[b.Key]
		if !retracted {
			out = append(out, beliefLines(b, "")...)
			continue
		}
		// The evidence the retraction rested on comes first; what has been
		// seen since is what may reinstate it.
		out = append(out, beliefLines(r.Evidence, "")...)
		out = append(out, fmt.Sprintf("    retracted %s (decision %s): %s now goes back to advice. Undo: tokenops coach reinstate %s",
			r.At.Format("2006-01-02 15:04"), r.ID, moveLabel(r.Key), r.Key.Kind))
		out = append(out, beliefLines(b, "    ")...)
	}
	for _, r := range retractions {
		if !r.Active() {
			out = append(out, fmt.Sprintf("%s reinstated %s by %s: %s", moveLabel(r.Key), r.Ended.Format("2006-01-02"), r.EndedBy, r.EndedWhy))
		}
	}
	return out
}

func beliefLines(b Belief, indent string) []string {
	short := trialNounShort(b.Key)
	head := fmt.Sprintf("%s, %s → %s: %d %s, %d judged against %d comparable %s",
		kindLabel(b.Key.Kind), b.Key.From, b.Key.To, b.Moves, plural(b.Moves, "move"), b.Judged, b.Baseline, plural(b.Baseline, short))
	if b.AfterRetraction {
		head = fmt.Sprintf("since the retraction, %d %s on %s judged against %d comparable %s",
			b.Judged, plural(b.Judged, short), b.Key.To, b.Baseline, plural(b.Baseline, short))
	}
	var out []string
	switch b.Verdict {
	case VerdictUnjudged:
		out = append(out, indent+head+"; not judged yet: "+b.Why)
	case VerdictHolds:
		out = append(out, fmt.Sprintf("%s%s; %s had a bad outcome vs %s; holds: %s", indent, head, pct(b.Rate), pct(b.BaselineRate), b.Why))
	case VerdictDropped:
		out = append(out, fmt.Sprintf("%s%s; %s had a bad outcome vs %s; quality dropped: %s", indent, head, pct(b.Rate), pct(b.BaselineRate), b.Why))
	}
	if s := signalText(b.Signals); s != "" && b.Judged > 0 {
		out = append(out, indent+"    what went wrong: "+s)
	}
	return out
}

// Headline is the one line the operator is told when a move is
// retracted.
func Headline(r Retraction) string {
	return fmt.Sprintf("tokenops: stopped %s on its own: %s did worse there (%s went wrong against %s). It advises instead; `tokenops explain moves` shows the evidence, `tokenops coach reinstate %s` undoes this.",
		moveLabel(r.Key), r.Key.To, pct(r.Evidence.Rate), pct(r.Evidence.BaselineRate), r.Key.Kind)
}

func moveLabel(k Key) string {
	if k.Kind == KindDefaultModel {
		return fmt.Sprintf("starting new sessions on %s instead of %s", k.To, k.From)
	}
	return fmt.Sprintf("moving %s subagents from %s to %s", k.Kind, k.From, k.To)
}

func kindLabel(kind string) string {
	if kind == KindDefaultModel {
		return "new sessions"
	}
	return kind + " work"
}

func runNoun(k Key) string {
	if k.Kind == KindDefaultModel {
		return "session"
	}
	return "run"
}

func trialNoun(k Key) string {
	if k.Kind == KindDefaultModel {
		return "instructions"
	}
	return "subagent runs"
}

func trialNounShort(k Key) string {
	if k.Kind == KindDefaultModel {
		return "instruction"
	}
	return "run"
}

func signalText(s Signals) string {
	var parts []string
	for _, x := range []struct {
		n    int
		what string
	}{
		{s.Failed, "failed"}, {s.Rerun, "re-run by the agent"}, {s.Reworked, "reworked"},
		{s.Interrupted, "interrupted"}, {s.Rejected, "rejected by you"},
	} {
		if x.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", x.n, x.what))
		}
	}
	return strings.Join(parts, ", ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

func pct(f float64) string { return fmt.Sprintf("%.0f%%", f*100) }

func points(f float64) int { return int(math.Round(f * 100)) }
