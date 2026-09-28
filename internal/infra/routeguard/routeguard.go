// Package routeguard decides, once per turn, whether the model a session
// is running on still fits the work it has been asked to do.
//
// An operator picks a model at the start of a session and it stays
// picked. Every turn after that runs on it, including the retrieval and
// the reading-around that a cheaper, faster model would do just as well.
// Nothing in the client asks the question again, so the choice made for
// the first turn silently governs the hundredth.
//
// This asks it again. It only ever suggests routing down, because
// suggesting a pricier model is a spending decision an operator did not
// delegate, and on a subscription it can walk them into a cap they did
// not choose. It argues a case once per kind of work per session rather
// than every turn, because advice repeated on every prompt stops being
// read.
package routeguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/modeltier"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/taskclass"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Mode is how hard the guard pushes.
type Mode string

// The modes, least assertive first.
const (
	// ModeOff disables the guard.
	ModeOff Mode = "off"
	// ModeAdvise states the case and changes nothing.
	ModeAdvise Mode = "advise"
	// ModeDelegate additionally marks work the caller may hand to a
	// subagent on the cheaper model, for the kinds an operator allowed.
	ModeDelegate Mode = "delegate"
	// ModeAuto treats every kind it is confident about as delegable.
	ModeAuto Mode = "auto"
)

// ParseMode reads a configured mode, defaulting to advise. An
// unrecognised value is not silently treated as "off": a typo should
// leave the guard talking, not quietly disable it.
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "false", "no":
		return ModeOff
	case "delegate":
		return ModeDelegate
	case "auto":
		return ModeAuto
	default:
		return ModeAdvise
	}
}

// Input is one turn's question.
type Input struct {
	// Dir holds per-session state. Created when missing.
	Dir       string
	SessionID string
	// Prompt is the operator's instruction for this turn.
	Prompt string
	// CurrentModel is what the session is running on.
	CurrentModel string
	Provider     eventschema.Provider
	Mode         Mode
	// Verbosity is how much the guard says (ADR 0006): quiet argues only
	// a mismatch of two tiers or more, verbose argues every applicable
	// turn, and empty (normal) argues once per kind of work per session.
	Verbosity string
	// AutoKinds are the kinds that may be delegated in ModeDelegate.
	// Ignored in ModeAuto, which allows every confident kind.
	AutoKinds []taskclass.Kind
	Catalog   *modeltier.Catalog
	// Candidates are the models actually on offer for this provider.
	//
	// Required. Without it the catalog ranks across every model the
	// vendor ever priced, and the cheapest row is a model retired years
	// ago — the guard would confidently recommend it. Abstaining is the
	// honest answer to "what should this run on" when nobody has said
	// what is available.
	Candidates []string
	Now        time.Time
	// Quieted reports that advice for a kind of work has been ignored
	// often enough that the coach stops offering it (ADR 0006,
	// follow-through). Verbose still advises. Nil quiets nothing.
	Quieted func(taskclass.Kind) bool
	// NewID names a new piece of advice so its outcome can be recorded
	// against it. Nil records nothing.
	NewID func() string
}

// Decision is what the guard concluded.
type Decision struct {
	// Advise reports that the case is worth putting to the operator.
	Advise bool
	// Delegate reports that the caller may run this turn's work on To
	// without asking first.
	Delegate bool
	Kind     taskclass.Kind
	From     string
	To       string
	Reason   string
	// OfferID names this turn's advice when it is new, for the
	// follow-through ledger. Empty when nothing new was advised.
	OfferID string
	// Quieted reports that advice was due and withheld because earlier
	// advice of this kind kept being ignored.
	Quieted bool
	// Resolved are earlier pieces of advice whose outcome this turn
	// settled.
	Resolved []Resolution
}

// Resolution is the outcome of one earlier piece of advice.
type Resolution struct {
	ID       string
	Kind     taskclass.Kind
	Followed bool
	Evidence string
}

// AdviceWindow is how many later turns advice waits to be followed before
// it counts as ignored. Claude Code writes the model a turn ran on only
// once the turn is answered, so a /model switch shows up a turn late and
// the window allows for it.
const AdviceWindow = 4

// openAdvice is advice still waiting to be followed.
type openAdvice struct {
	ID string `json:"id"`
	// FromRank is the tier the session was on when advised. Following
	// means running below it.
	FromRank int    `json:"from_rank"`
	To       string `json:"to"`
	Turns    int    `json:"turns"`
}

// state is the per-session memory: the kind of work in flight, and which
// kinds have already been argued.
type state struct {
	Kind   taskclass.Kind                 `json:"kind"`
	Argued map[taskclass.Kind]bool        `json:"argued"`
	Open   map[taskclass.Kind]*openAdvice `json:"open,omitempty"`
	// Proposals are subagent moves put to the operator (models: ask).
	Proposals []Proposal `json:"proposals,omitempty"`
}

// tierForKind maps what the work is to the capability it needs.
//
// Retrieval wants the fastest thing that can read; research wants a
// capable generalist and not a flagship; a routine edit sits in the
// middle; and the deep work is what a flagship is for.
func tierForKind(k taskclass.Kind) (modeltier.Tier, bool) {
	switch k {
	case taskclass.KindLookup:
		return modeltier.TierLookup, true
	case taskclass.KindResearch:
		return modeltier.TierBalanced, true
	case taskclass.KindEdit:
		return modeltier.TierDefault, true
	case taskclass.KindDeep:
		return modeltier.TierDeep, true
	default:
		return modeltier.TierUnknown, false
	}
}

// tierRank orders tiers so "cheaper than" is expressible here too.
var tierRank = map[modeltier.Tier]int{
	modeltier.TierLookup: 1, modeltier.TierBalanced: 2,
	modeltier.TierDefault: 3, modeltier.TierDeep: 4,
}

// Evaluate answers one turn.
func Evaluate(in Input) Decision {
	if in.Mode == ModeOff || in.Catalog == nil || len(in.Candidates) == 0 {
		return Decision{}
	}
	st := loadState(in.Dir, in.SessionID)
	kind := taskclass.KindForTurn(in.Prompt, st.Kind)
	st.Kind = kind
	if st.Argued == nil {
		st.Argued = map[taskclass.Kind]bool{}
	}
	// A closure, not defer saveState(..., st): deferred arguments are
	// evaluated at the defer statement, which would persist the state as
	// it was before this turn decided anything.
	//
	// State is written even when nothing is advised, because the kind in
	// flight is what the next continuation inherits and dropping it
	// would make every "go" start again from nothing.
	defer func() { saveState(in.Dir, in.SessionID, st) }()

	d := Decision{Kind: kind, From: in.CurrentModel}
	cat := in.Catalog.WithCandidates(withCurrent(in.Candidates, in.CurrentModel))
	cur := cat.Resolve(in.Provider, in.CurrentModel)
	d.Resolved = st.observe(tierRank[cur.Tier], in.CurrentModel, "the session moved to ")
	wantTier, ok := tierForKind(kind)
	if !ok {
		return d
	}
	if cur.Tier == modeltier.TierUnknown {
		// An unplaceable model is not an invitation to guess what it is
		// worth replacing with.
		return d
	}
	if tierRank[wantTier] >= tierRank[cur.Tier] {
		// Either the model fits, or the work wants more than it. Routing
		// up is a spending decision nobody delegated.
		return d
	}
	target, ok := cat.Target(in.Provider, wantTier)
	if !ok {
		// The menu may not hold a model on that exact band — three
		// models with one free leaves two priced rows, which can only
		// express cheapest and dearest. Fall back to the most capable
		// thing still cheaper than what the turn is on, which is the
		// question that still has an answer.
		target, ok = cat.TargetBelow(in.Provider, cur.Tier)
	}
	if !ok || target == in.CurrentModel {
		return d
	}
	d.To = target
	d.Reason = fmt.Sprintf("this turn is %s work; %s is the %s tier and %s is on %s",
		kind, target, wantTier, in.CurrentModel, cur.Tier)

	switch in.Verbosity {
	case "quiet":
		d.Advise = tierRank[cur.Tier]-tierRank[wantTier] >= 2 && !st.Argued[kind]
	case "verbose":
		d.Advise = true
	default:
		d.Advise = !st.Argued[kind]
	}
	if d.Advise && in.Verbosity != "verbose" && in.Quieted != nil && in.Quieted(kind) {
		d.Advise, d.Quieted = false, true
	}
	if d.Advise {
		st.Argued[kind] = true
		d.OfferID = st.offer(kind, tierRank[cur.Tier], target, in.NewID)
	}
	d.Delegate = delegable(in, kind)
	return d
}

// offer opens a piece of advice for kind and returns its ID. Advice that is
// already open is not reopened: repeating it is not a new offer.
func (st *state) offer(kind taskclass.Kind, fromRank int, to string, newID func() string) string {
	if newID == nil {
		return ""
	}
	if st.Open == nil {
		st.Open = map[taskclass.Kind]*openAdvice{}
	}
	if st.Open[kind] != nil {
		return ""
	}
	id := newID()
	st.Open[kind] = &openAdvice{ID: id, FromRank: fromRank, To: to}
	return id
}

// observe settles open advice against the model a turn ran on: running
// below the tier the advice was given on is following it, and AdviceWindow
// turns without that is ignoring it. rank 0 is a model that cannot be
// placed, which follows nothing.
func (st *state) observe(rank int, model, how string) []Resolution {
	var out []Resolution
	for kind, o := range st.Open {
		switch {
		case rank > 0 && rank < o.FromRank:
			out = append(out, Resolution{ID: o.ID, Kind: kind, Followed: true, Evidence: how + model})
		case o.Turns+1 >= AdviceWindow:
			out = append(out, Resolution{ID: o.ID, Kind: kind, Evidence: fmt.Sprintf("still on the same tier %d turns later", AdviceWindow)})
		default:
			o.Turns++
			continue
		}
		delete(st.Open, kind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ObserveSubagent settles open advice in a session when the agent launches
// a subagent on a model below the tier the advice was given on: handing
// the work to a cheaper subagent is one of the two ways to follow it.
//
// model is the model the agent asked for, before any rewrite by the coach,
// so the coach's own moves never count as the operator following advice.
func ObserveSubagent(dir, session, model string, provider eventschema.Provider, catalog *modeltier.Catalog, candidates []string) []Resolution {
	if dir == "" || model == "" || catalog == nil || len(candidates) == 0 {
		return nil
	}
	st := loadState(dir, session)
	if len(st.Open) == 0 {
		return nil
	}
	resolved := resolveAlias(model, candidates)
	rank := tierRank[catalog.WithCandidates(candidates).Resolve(provider, resolved).Tier]
	var out []Resolution
	for kind, o := range st.Open {
		if rank > 0 && rank < o.FromRank {
			out = append(out, Resolution{ID: o.ID, Kind: kind, Followed: true, Evidence: "a subagent ran on " + resolved})
			delete(st.Open, kind)
		}
	}
	if len(out) > 0 {
		saveState(dir, session, st)
	}
	return out
}

// delegable reports whether this turn may be moved without asking.
func delegable(in Input, kind taskclass.Kind) bool {
	switch in.Mode {
	case ModeAuto:
		return true
	case ModeDelegate:
		for _, k := range in.AutoKinds {
			if k == kind {
				return true
			}
		}
	}
	return false
}

func statePath(dir, session string) string {
	name := session
	if name == "" {
		name = "unknown"
	}
	return filepath.Join(dir, "session-"+filepath.Base(name)+".json")
}

func loadState(dir, session string) state {
	var st state
	b, err := os.ReadFile(statePath(dir, session)) //nolint:gosec // operator's own state dir
	if err != nil {
		return st
	}
	_ = json.Unmarshal(b, &st)
	return st
}

// saveState writes the session's state, creating the directory first.
// A hook that writes into a directory nobody created reports success and
// stores nothing, which is how the last one of these latched no state at
// all while appearing to work.
func saveState(dir, session string, st state) {
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = os.WriteFile(statePath(dir, session), b, 0o600)
}

// withCurrent adds the model a session is running on to the models on
// offer. It is available by definition, and leaving it out makes the
// guard abstain on every turn of a session whose model is newer than the
// configured list: claude-opus-5-5 sessions against a list naming
// claude-opus-5 got no advice at all.
func withCurrent(candidates []string, current string) []string {
	if current == "" || len(candidates) == 0 {
		return candidates
	}
	for _, c := range candidates {
		if c == current {
			return candidates
		}
	}
	return append(slices.Clone(candidates), current)
}
