// Package policy is the one control ladder: how much authority TokenOps
// has been given to act without being asked.
//
// The code had four ladders, none of which agreed:
//
//	config.Mode              passive | active                 (daemon-wide)
//	CoachingConfig.Delivery  observe | advise | intervene
//	readguard.Mode           observe | active
//	routingapproval          a propose/decide gate, expressed as neither
//
// Three vocabularies for the same idea, plus one concept — requiring a
// human decision — that only routing could express at all. "Active"
// means two different things depending on which setting you are reading.
// An operator asking "what can TokenOps do without me" had to assemble
// the answer from four places and reconcile the words themselves.
//
// The ladder here is the intent's: observe-only → recommend → require
// approval → automatic. Each subsystem keeps its own configuration
// syntax — changing those is a breaking change for no gain — and
// expresses its authority in these terms.
//
// # Fail silent, not open
//
// ObserveOnly is the zero value, and every mapping sends an unrecognised
// setting there. A control system that grants authority when it cannot
// read its configuration is worse than one that grants none: the
// failure is invisible precisely when it matters.
package policy

import (
	"fmt"
	"strings"
)

// Authority is one rung of the control ladder.
type Authority string

const (
	// ObserveOnly — record and answer when asked. Says nothing
	// unprompted, changes nothing. The zero value.
	ObserveOnly Authority = ""
	// Recommend — may say what it would do, unprompted. Still changes
	// nothing.
	Recommend Authority = "recommend"
	// RequireApproval — may act, once a person agrees to each action.
	// The rung only routingapproval could previously express.
	RequireApproval Authority = "require_approval"
	// Automatic — may act without asking.
	Automatic Authority = "automatic"
)

// rank orders the ladder.
func (a Authority) rank() int {
	switch a {
	case Recommend:
		return 1
	case RequireApproval:
		return 2
	case Automatic:
		return 3
	default:
		return 0
	}
}

// AtLeast reports whether this rung carries at least the authority of
// another.
func (a Authority) AtLeast(other Authority) bool { return a.rank() >= other.rank() }

// MaySpeak reports whether the subsystem may say something unprompted.
func (a Authority) MaySpeak() bool { return a.rank() >= Recommend.rank() }

// MayAct reports whether the subsystem may change what happens, with or
// without asking first. Callers that can act must also check
// NeedsApproval.
func (a Authority) MayAct() bool { return a.rank() >= RequireApproval.rank() }

// NeedsApproval reports whether each action needs a person to agree.
func (a Authority) NeedsApproval() bool { return a == RequireApproval }

// String renders the rung. ObserveOnly renders as a word rather than the
// empty string it is stored as, so an operator never reads a blank.
func (a Authority) String() string {
	if a == ObserveOnly {
		return "observe_only"
	}
	return string(a)
}

// Describe explains the rung in the terms an operator decides in.
func (a Authority) Describe() string {
	switch a {
	case Recommend:
		return "says what it would do, unprompted; changes nothing"
	case RequireApproval:
		return "acts, but only on actions you have agreed to"
	case Automatic:
		return "acts without asking"
	default:
		return "records and answers when asked; says nothing unprompted, changes nothing"
	}
}

// Ladder returns the rungs in ascending order, for a surface offering
// the choice.
func Ladder() []Authority {
	return []Authority{ObserveOnly, Recommend, RequireApproval, Automatic}
}

// Parse reads a rung, refusing anything it does not recognise.
//
// Unlike the FromX mappings, this errors rather than defaulting:
// configuration validation should tell an operator their setting is
// wrong, where a runtime read of an already-validated field should stay
// on the safe rung.
func Parse(s string) (Authority, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "observe_only", "observe-only", "observe", "":
		return ObserveOnly, nil
	case "recommend":
		return Recommend, nil
	case "require_approval", "require-approval", "approval":
		return RequireApproval, nil
	case "automatic", "auto":
		return Automatic, nil
	}
	return ObserveOnly, fmt.Errorf(
		"policy: %q is not a control rung; use one of observe_only, recommend, "+
			"require_approval, automatic", s)
}

// Effective is the authority a subsystem actually has: the lesser of the
// daemon's and its own.
//
// This is the property the four separate ladders could not offer.
// Turning the daemon down now turns everything down, which is what an
// operator reaching for `mode: passive` in a hurry already believes it
// does.
func Effective(daemon, subsystem Authority) Authority {
	if daemon.rank() < subsystem.rank() {
		return daemon
	}
	return subsystem
}

// FromDaemonMode maps config.Mode (passive | active) onto the ladder.
//
// Active becomes Automatic: the field is documented as the daemon
// applying routing rules to live traffic, which is acting without
// asking.
func FromDaemonMode(mode string) Authority {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "active":
		return Automatic
	default:
		return ObserveOnly
	}
}

// FromCoachingDelivery maps CoachingConfig.Delivery onto the ladder.
//
// This is the only existing ladder with three rungs, and the only one
// whose middle rung is genuinely "recommend": advise is documented as
// the coach speaking unprompted but never acting.
func FromCoachingDelivery(delivery string) Authority {
	switch strings.ToLower(strings.TrimSpace(delivery)) {
	case "advise":
		return Recommend
	case "intervene":
		return Automatic
	default:
		return ObserveOnly
	}
}

// FromReadGuardMode maps readguard.Mode onto the ladder.
//
// Its words are the daemon's and mean something else: readguard's
// "observe" records a would_block it deliberately does not credit, and
// its "active" refuses the read outright.
func FromReadGuardMode(mode string) Authority {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "active":
		return Automatic
	default:
		return ObserveOnly
	}
}

// FromRoutingApproval maps the routing gate onto the ladder.
//
// A gated router is the require-approval rung, which is the concept no
// other ladder could express — and the reason routing had to invent its
// own shape rather than reuse one.
func FromRoutingApproval(gated bool) Authority {
	if gated {
		return RequireApproval
	}
	return Automatic
}

// FromSmartRoutingIntervention maps smart_routing.intervention onto the
// ladder.
//
// This is a fifth ladder with a fourth vocabulary, and the audit that
// produced ADR 0004 counted four. It is the only one whose default is
// not the bottom rung: the config documents an empty value as "advise",
// so a machine that enabled smart routing without naming an
// intervention already has a subsystem speaking unprompted.
//
// "delegate" maps to RequireApproval rather than Automatic. It marks
// work that *may* be handed to a subagent on a cheaper model — a
// proposal awaiting a decision, not an action taken. Reporting it as
// automatic would claim more authority than the operator granted, and
// overstating authority is the direction that matters: an operator who
// believes TokenOps is acting will look for effects that are not there,
// and one who believes it is not will be surprised by effects that are.
func FromSmartRoutingIntervention(intervention string) Authority {
	switch strings.ToLower(strings.TrimSpace(intervention)) {
	case "", "advise":
		return Recommend
	case "delegate":
		return RequireApproval
	case "auto":
		return Automatic
	default:
		// "off" and anything unreadable.
		return ObserveOnly
	}
}
