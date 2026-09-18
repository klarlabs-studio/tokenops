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
}

// state is the per-session memory: the kind of work in flight, and which
// kinds have already been argued.
type state struct {
	Kind   taskclass.Kind          `json:"kind"`
	Argued map[taskclass.Kind]bool `json:"argued"`
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
	wantTier, ok := tierForKind(kind)
	if !ok {
		return d
	}
	cat := in.Catalog.WithCandidates(in.Candidates)
	cur := cat.Resolve(in.Provider, in.CurrentModel)
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

	if !st.Argued[kind] {
		d.Advise = true
		st.Argued[kind] = true
	}
	d.Delegate = delegable(in, kind)
	return d
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
