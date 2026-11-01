package coach

import (
	"os"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	mv "go.klarlabs.de/tokenops/internal/contexts/coaching/moveverdict"
	"go.klarlabs.de/tokenops/internal/infra/modellever"
	"go.klarlabs.de/tokenops/internal/infra/moveverify"
	"go.klarlabs.de/tokenops/internal/infra/routeguard"
)

// judgeEvery is how often the evidence is read and judged at most. Every
// turn only notes the session (its start, against the default-model
// lever), which reads a few lines; reading transcripts and judging runs
// in one pass every judgeEvery across all sessions. Nothing it watches
// settles faster: a subagent's outcome stays open for mv.Settle.
const judgeEvery = 10 * time.Minute

// maxRevisits bounds how many other sessions one pass reads, so a pass
// after a long pause stays short; the rest are read on the next.
const maxRevisits = 20

// VerifyEnv is what verifying the coach's moves reads.
type VerifyEnv struct {
	Store moveverify.Store
	// Session and Transcript are the Claude Code session whose turn just
	// ended.
	Session, Transcript string
	Ledger              Ledger
	Lever               modellever.Paths
	Verbosity           string
	Now                 time.Time
}

// VerifyMoves verifies the coach's autonomous model moves from the Stop
// hook. Each turn it notes the session; at most every judgeEvery it reads
// what became of the work in every session that changed (subagent
// outcomes, and each session's agent DX against the default-model lever)
// and judges every move kind against comparable unmoved work: a kind
// whose quality dropped is retracted, so the coach stops making it on its
// own, and a retracted default-model move is reverted at once. It returns
// what to tell the operator, once per retraction, "" for nothing. It
// reads local files only and fails open.
func VerifyMoves(e VerifyEnv) string {
	if e.Store.Dir == "" || e.Session == "" || e.Transcript == "" {
		return ""
	}
	cur := e.Store.LoadSession(e.Session)
	if !cur.LeverChecked || cur.Transcript != e.Transcript {
		if !cur.LeverChecked {
			start := routeguard.ReadStart(e.Transcript)
			if start.At.IsZero() {
				return ""
			}
			// The lever is read at the session's first turn, when its
			// state is the one the session started under.
			cur.LeverChecked, cur.Lever = true, leverAtStart(e.Lever, start)
		}
		cur.Transcript = e.Transcript
		_ = e.Store.SaveSession(cur)
	}
	if e.Now.Sub(e.Store.Load().JudgedAt) < judgeEvery {
		return ""
	}
	moves := subagentMoves(e.Ledger)
	sessions := e.Store.Sessions(e.Now, true)
	revisited := 0
	for i, f := range sessions {
		switch {
		case f.Session == e.Session:
		case revisited < maxRevisits && f.Transcript != "" && (f.Pending || changedSince(f.Transcript, f.Updated)):
			revisited++
		default:
			continue
		}
		sessions[i] = observeSession(e, f, moves)
	}
	var obs []mv.Observation
	for _, f := range sessions {
		obs = append(obs, f.Observations()...)
	}
	var (
		told    []string
		reverts []mv.Retraction
	)
	_ = e.Store.Update(func(st *moveverify.State) error {
		st.JudgedAt = e.Now.UTC()
		for _, c := range mv.Decide(mv.Judge(obs, st.Retractions, e.Now), st.Retractions, e.Now, NewID) {
			switch {
			case c.Retract != nil:
				r := *c.Retract
				r.Surfaced = true
				st.Retractions = append(st.Retractions, r)
				told = append(told, mv.Headline(r))
				if r.Key.Kind == mv.KindDefaultModel {
					reverts = append(reverts, r)
				}
			case c.Reinstate != nil:
				for i := range st.Retractions {
					if st.Retractions[i].ID == c.Reinstate.ID {
						st.Retractions[i] = *c.Reinstate
					}
				}
			}
		}
		return nil
	})
	for _, r := range reverts {
		if revertRetractedLever(e, r) {
			told = append(told, "tokenops: new Claude Code sessions start on your own default model again.")
		}
	}
	return quietOr(e.Verbosity, strings.Join(told, "\n"))
}

// changedSince reports whether the transcript was written after t.
func changedSince(path string, t time.Time) bool {
	info, err := os.Stat(path)
	return err == nil && info.ModTime().After(t)
}

// observeSession reads one session's transcript, in one pass, into its
// evidence file: its settled subagent calls and its first instructions on
// the model it started on.
func observeSession(e VerifyEnv, f moveverify.SessionFile, moves map[string]mv.Move) moveverify.SessionFile {
	tl, start := routeguard.ReadTimeline(f.Transcript)
	if start.At.IsZero() {
		return f
	}
	if tl.Session == "" {
		tl.Session = f.Session
	}
	obs, pending := mv.ObserveCalls(tl, moves, e.Now)
	f.Merge(obs)
	f.Pending = pending
	if units := mv.UnitOutcomes(tl, tl.ModelSwitch); start.Model != "" && len(units) > 0 {
		o := mv.ObserveSession(f.Session, tl.Scope, start.At, start.Model, units, f.Lever)
		f.Start = &o
	}
	f.Updated = e.Now.UTC()
	_ = e.Store.SaveSession(f)
	return f
}

// leverAtStart is the default-model move a session started under: the
// lever was set before the session began and the session runs on the
// model it set. nil when it did not.
func leverAtStart(p modellever.Paths, start routeguard.SessionStart) *mv.Move {
	cur, res := modellever.Current(p)
	if cur == nil || res.Status != modellever.StatusActive || start.At.Before(cur.At) {
		return nil
	}
	to := routeguard.AliasFor(cur.Value)
	if to == "" || start.Model != to {
		return nil
	}
	return &mv.Move{ID: cur.MoveID, Kind: mv.KindDefaultModel, From: routeguard.AliasFor(cur.From), To: to}
}

// revertRetractedLever removes the default model tokenops set when it is
// the move r retracted, and records the move as undone, with why, in the
// follow-through ledger. A model the operator set is never touched.
func revertRetractedLever(e VerifyEnv, r mv.Retraction) bool {
	cur, res := modellever.Current(e.Lever)
	if cur == nil || res.Status != modellever.StatusActive ||
		routeguard.AliasFor(cur.From) != r.Key.From || routeguard.AliasFor(cur.Value) != r.Key.To {
		return false
	}
	out, err := modellever.Revert(e.Lever)
	if err != nil || out.Status != modellever.StatusReverted {
		return false
	}
	if e.Ledger != nil && cur.MoveID != "" {
		_ = e.Ledger.Append(ft.Entry{
			Type: ft.EntryResolve, ID: cur.MoveID, At: e.Now.UTC(), Kind: DefaultModelKind,
			Outcome: ft.OutcomeUndone, Evidence: "tokenops retracted it (decision " + r.ID + "): " + r.Reason,
		})
	}
	return true
}

// subagentMoves are the subagent moves the coach made, put to the
// operator, or held back at random, by the Agent call's tool_use_id, with
// model families.
func subagentMoves(l Ledger) map[string]mv.Move {
	out := map[string]mv.Move{}
	if l == nil {
		return out
	}
	entries, err := l.Load()
	if err != nil {
		return out
	}
	for _, en := range entries {
		if en.Ref == "" || en.Power != config.PowerModels {
			continue
		}
		held := en.Type == ft.EntryHoldout
		if !held && (en.Type != ft.EntryOffer || (en.Channel != ft.ChannelMove && en.Channel != ft.ChannelApproval)) {
			continue
		}
		out[en.Ref] = mv.Move{ID: en.ID, Kind: en.Kind, From: routeguard.AliasFor(en.From), To: routeguard.AliasFor(en.To), Held: held}
	}
	return out
}

// Reinstate ends the active retraction of every move kind of kind ("all"
// for every kind) on the operator's word, and returns the ones it ended.
func Reinstate(s moveverify.Store, kind string, now time.Time) ([]mv.Retraction, error) {
	var ended []mv.Retraction
	err := s.Update(func(st *moveverify.State) error {
		for i, r := range st.Retractions {
			if !r.Active() || (kind != "all" && r.Key.Kind != kind) {
				continue
			}
			r.Ended, r.EndedBy, r.EndedWhy = now.UTC(), mv.EndedByOperator, "you reinstated it"
			st.Retractions[i] = r
			ended = append(ended, r)
		}
		return nil
	})
	return ended, err
}

// MoveVerdicts is, per kind of autonomous move, what the evidence says:
// the beliefs, the retractions, and both in plain words.
type MoveVerdicts struct {
	Beliefs     []mv.Belief     `json:"beliefs,omitempty"`
	Retractions []mv.Retraction `json:"retractions,omitempty"`
	Lines       []string        `json:"lines,omitempty"`
}

// Verdicts judges the evidence in s as it stands, without deciding
// anything: what `tokenops explain moves` and the coach's status show.
func Verdicts(s moveverify.Store, now time.Time) MoveVerdicts {
	var obs []mv.Observation
	for _, f := range s.Sessions(now, false) {
		obs = append(obs, f.Observations()...)
	}
	rs := s.Load().Retractions
	bs := mv.Judge(obs, rs, now)
	return MoveVerdicts{Beliefs: bs, Retractions: rs, Lines: mv.Lines(bs, rs)}
}

// MoveStore is the move-verify store Status reads; a variable so tests do
// not read the machine's.
var MoveStore = func() moveverify.Store {
	s, err := moveverify.Default()
	if err != nil {
		return moveverify.Store{}
	}
	return s
}
