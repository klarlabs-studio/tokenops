// Package followthrough records whether the coach's interventions were
// followed (ADR 0006, decision 6).
//
// A tip or a piece of advice that nobody acts on is noise whether or not it
// is correct: ADR 0005 calls that the effective false-positive rate. An
// action the coach takes on its own either stands or is undone. This
// package holds those outcomes as an append-only ledger of entries and
// folds them into per-kind evidence, which is what lets the coach go quiet
// on advice the operator keeps ignoring and what ADR 0005's promotion bar
// reads.
//
// It is pure: callers load and append entries through a port.
package followthrough

import (
	"sort"
	"time"
)

// Channel is how the coach intervened.
type Channel string

// The channels.
const (
	// ChannelAdvice is a suggested change the operator may make, such as
	// moving the session to a cheaper model.
	ChannelAdvice Channel = "advice"
	// ChannelTip is information about the session, such as a quota
	// window filling up.
	ChannelTip Channel = "tip"
	// ChannelMove is a change the coach made itself.
	ChannelMove Channel = "move"
	// ChannelApproval is a change the coach proposed and the operator
	// approved (followed) or declined (ignored).
	ChannelApproval Channel = "approval"
)

// Outcome is what became of one intervention.
type Outcome string

// The outcomes. Followed and Ignored are recorded; Stood, Undone, Open,
// Unknown, and Unseen are derived when the ledger is folded; the
// operator's answers (lifecycle.go) are given.
const (
	OutcomeFollowed Outcome = "followed"
	OutcomeIgnored  Outcome = "ignored"
	OutcomeStood    Outcome = "stood"
	OutcomeUndone   Outcome = "undone"
	// OutcomeOpen is an intervention still inside its window.
	OutcomeOpen Outcome = "open"
	// OutcomeUnknown is advice whose session ended before its window
	// closed: nothing shows whether it would have been followed, so it
	// counts as neither.
	OutcomeUnknown Outcome = "unknown"
	// OutcomeUnseen is a tip nothing shows the operator saw, which was
	// not acted on. A tip delivered only as a Stop hook's systemMessage
	// is one dim line after the turn, easy to miss and absent when the
	// session is driven remotely, so letting it pass is no evidence it
	// was turned down: it counts as neither followed nor ignored.
	OutcomeUnseen Outcome = "unseen"
)

// Via names where a tip reached the operator.
const (
	// ViaStopHook is the Stop hook's systemMessage, shown once after
	// the turn. It is no evidence the tip was read.
	ViaStopHook = "stop_hook"
	// ViaStatusLine is the agent's status line, which the operator sees
	// on every turn. A tip shown there counts as seen.
	ViaStatusLine = "statusline"
	// ViaAmbient is a tip that was due but went only to the status line,
	// not interrupting: past the day's interruption budget, or a further
	// step in an episode already told.
	ViaAmbient = "ambient"
)

// EntryType is what one ledger line records.
type EntryType string

// The entry types.
const (
	// EntryOffer is an intervention the coach made.
	EntryOffer EntryType = "offer"
	// EntryResolve is the recorded outcome of an earlier offer.
	EntryResolve EntryType = "resolve"
	// EntryLowered is the operator lowering a power's autonomy. It undoes
	// that power's recent autonomous moves.
	EntryLowered EntryType = "lowered"
	// EntrySeen records that an offer was shown somewhere the operator
	// sees, such as the status line. It carries the offer's ID.
	EntrySeen EntryType = "seen"
	// EntryHoldout is a move the coach would have made and held back at
	// random, to measure the moves it does make against. It is not an
	// intervention, so it is never folded as one.
	EntryHoldout EntryType = "holdout"
)

// Entry is one ledger line.
type Entry struct {
	Type EntryType `json:"type"`
	// ID names an offer; a resolve carries the ID of the offer it closes.
	ID      string    `json:"id,omitempty"`
	At      time.Time `json:"at"`
	Session string    `json:"session,omitempty"`
	// Power is inform, waste, or models.
	Power   string  `json:"power,omitempty"`
	Channel Channel `json:"channel,omitempty"`
	// Kind is the finding or the kind of work the offer was about
	// ("lookup", "quota_90").
	Kind string `json:"kind,omitempty"`
	// From and To are the change offered or made (models), or, on a
	// lowered entry, the power's effective rung before and after.
	From     string  `json:"from,omitempty"`
	To       string  `json:"to,omitempty"`
	Outcome  Outcome `json:"outcome,omitempty"`
	Evidence string  `json:"evidence,omitempty"`
	// Via is, on an offer, where it was delivered (ViaStopHook) and, on
	// a seen entry, where it was seen (ViaStatusLine).
	Via string `json:"via,omitempty"`
	// Until is, on a snooze, when the snooze ends.
	Until time.Time `json:"until,omitzero"`
	// Ref is, on a subagent move or approval, the Agent call's
	// tool_use_id, so the call's outcome can be read back from the
	// transcript and judged (moveverdict).
	Ref string `json:"ref,omitempty"`
}

// UndoWindow is how long after an autonomous move lowering the power
// still counts as undoing it. A move that outlives it stood.
const UndoWindow = 24 * time.Hour

// OpenFor is how long an advice or tip offer without a recorded outcome
// stays open. Past it the session has ended: a tip, whose levers include
// ending the session, was followed; advice has an unknown outcome.
const OpenFor = 24 * time.Hour

// Record is one offer with its outcome.
type Record struct {
	Offer      Entry     `json:"offer"`
	Outcome    Outcome   `json:"outcome"`
	ResolvedAt time.Time `json:"resolved_at,omitzero"`
	Evidence   string    `json:"evidence,omitempty"`
	// Seen reports evidence the operator saw the offer. Only tips need
	// it; advice and approvals are answered in the agent's own turn.
	Seen bool `json:"seen,omitempty"`
	// Answer is the operator's newest answer to a tip (acknowledged,
	// expected, snoozed), empty when they gave none; SnoozedUntil is
	// when a snooze ends.
	Answer       Outcome   `json:"answer,omitempty"`
	SnoozedUntil time.Time `json:"snoozed_until,omitzero"`
}

// Fold turns the ledger into one record per offer, oldest first.
func Fold(entries []Entry, now time.Time) []Record {
	resolved := map[string]Entry{}
	seen := map[string]bool{}
	answers := map[string]Entry{}
	var lowered []Entry
	for _, e := range entries {
		switch e.Type {
		case EntryResolve:
			if _, ok := resolved[e.ID]; !ok {
				resolved[e.ID] = e
			}
		case EntryLowered:
			lowered = append(lowered, e)
		case EntrySeen:
			seen[e.ID] = true
		case EntryFeedback:
			if Answerable(e.Outcome) {
				if a, ok := answers[e.ID]; !ok || !e.At.Before(a.At) {
					answers[e.ID] = e
				}
			}
		}
	}
	var out []Record
	for _, e := range entries {
		if e.Type != EntryOffer {
			continue
		}
		r := Record{Offer: e, Seen: seen[e.ID]}
		res, ok := resolved[e.ID]
		switch {
		case ok:
			r.Outcome, r.ResolvedAt, r.Evidence = res.Outcome, res.At, res.Evidence
		case e.Channel == ChannelMove:
			r.Outcome, r.ResolvedAt, r.Evidence = moveOutcome(e, lowered, now)
		case now.Sub(e.At) < OpenFor:
			r.Outcome = OutcomeOpen
		case e.Channel == ChannelTip:
			// A tip's levers include ending the session, and a session
			// that stopped before the tip's window closed did just that.
			r.Outcome, r.ResolvedAt, r.Evidence = OutcomeFollowed, e.At.Add(OpenFor), "the session ended"
		default:
			r.Outcome = OutcomeUnknown
		}
		if e.Channel == ChannelTip && !r.Seen &&
			(r.Outcome == OutcomeIgnored || (!ok && r.Outcome == OutcomeFollowed)) {
			// Nothing shows the tip was read. Letting it pass is not
			// turning it down, and a session that simply ended did not
			// follow it either.
			r.Outcome = OutcomeUnseen
		}
		if a, ok := answers[e.ID]; ok {
			r.Answer = a.Outcome
			if a.Outcome == OutcomeSnoozed {
				r.SnoozedUntil = a.Until
			}
			if r.Outcome != OutcomeFollowed {
				// The operator answered: whatever was inferred from their
				// silence, that is what became of the tip.
				r.Outcome, r.ResolvedAt = a.Outcome, a.At
			}
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Offer.At.Before(out[j].Offer.At) })
	return out
}

// moveOutcome decides whether an autonomous move stood: it is undone when
// the operator lowered its power within UndoWindow after it.
func moveOutcome(move Entry, lowered []Entry, now time.Time) (Outcome, time.Time, string) {
	for _, l := range lowered {
		if l.Power != move.Power || l.At.Before(move.At) || l.At.Sub(move.At) > UndoWindow {
			continue
		}
		return OutcomeUndone, l.At, "you set " + l.Power + " to " + l.To
	}
	if now.Sub(move.At) >= UndoWindow {
		return OutcomeStood, move.At.Add(UndoWindow), ""
	}
	return OutcomeOpen, time.Time{}, ""
}

// Summary is the evidence for one kind of intervention.
type Summary struct {
	Power    string  `json:"power"`
	Channel  Channel `json:"channel"`
	Kind     string  `json:"kind"`
	Followed int     `json:"followed"`
	Ignored  int     `json:"ignored"`
	Stood    int     `json:"stood"`
	Undone   int     `json:"undone"`
	Open     int     `json:"open"`
	Unknown  int     `json:"unknown"`
	// Unseen counts tips nothing shows the operator saw and that were
	// not acted on: neither followed nor ignored.
	Unseen int `json:"unseen"`
	// Acknowledged, Expected and Snoozed count the operator's answers.
	Acknowledged int `json:"acknowledged,omitempty"`
	Expected     int `json:"expected,omitempty"`
	Snoozed      int `json:"snoozed,omitempty"`
	// Quiet reports that the coach has stopped offering this kind because
	// it keeps being ignored (see Quieted).
	Quiet bool `json:"quiet,omitempty"`
}

// Resolved is how many offers of the kind have a known outcome.
func (s Summary) Resolved() int { return s.Followed + s.Ignored + s.Stood + s.Undone }

// Summarize groups records by power, channel, and kind, in a stable order.
func Summarize(records []Record, now time.Time) []Summary {
	type key struct {
		power   string
		channel Channel
		kind    string
	}
	idx := map[key]int{}
	var out []Summary
	for _, r := range records {
		k := key{r.Offer.Power, r.Offer.Channel, r.Offer.Kind}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, Summary{Power: k.power, Channel: k.channel, Kind: k.kind})
		}
		s := &out[i]
		switch r.Outcome {
		case OutcomeFollowed:
			s.Followed++
		case OutcomeIgnored:
			s.Ignored++
		case OutcomeStood:
			s.Stood++
		case OutcomeUndone:
			s.Undone++
		case OutcomeOpen:
			s.Open++
		case OutcomeUnknown:
			s.Unknown++
		case OutcomeUnseen:
			s.Unseen++
		case OutcomeAcknowledged:
			s.Acknowledged++
		case OutcomeExpected:
			s.Expected++
		case OutcomeSnoozed:
			s.Snoozed++
		}
	}
	for i := range out {
		out[i].Quiet = out[i].Channel != ChannelMove && Quieted(records, out[i].Power, out[i].Kind, now)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Power != out[j].Power {
			return out[i].Power < out[j].Power
		}
		if out[i].Channel != out[j].Channel {
			return out[i].Channel < out[j].Channel
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// The quieting rule's initial thresholds. ADR 0006 sets them from recorded
// evidence; these are the starting values that evidence will revise.
const (
	// QuietAfter is how many consecutive ignored offers of one kind make
	// the coach stop offering it.
	QuietAfter = 5
	// QuietLookback bounds the evidence. Once the ignored offers age out
	// the coach offers the kind again, so a changed habit gets a hearing.
	QuietLookback = 14 * 24 * time.Hour
)

// Quieted reports whether advice or tips of this power and kind have been
// ignored QuietAfter times in a row within QuietLookback, with nothing
// followed since. Offers without a known outcome do not count either way.
// Which kinds may go quiet at all is the caller's decision.
func Quieted(records []Record, power, kind string, now time.Time) bool {
	ignored := 0
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Offer.Channel == ChannelMove || r.Offer.Power != power || r.Offer.Kind != kind {
			continue
		}
		if now.Sub(r.Offer.At) > QuietLookback {
			break
		}
		switch r.Outcome {
		case OutcomeFollowed:
			return false
		case OutcomeIgnored:
			ignored++
			if ignored >= QuietAfter {
				return true
			}
		}
	}
	return false
}

// Prune drops entries older than keep, except that it never separates a
// resolve from the offer it closes: an offer's resolve is kept with it.
func Prune(entries []Entry, now time.Time, keep time.Duration) []Entry {
	kept := map[string]bool{}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Type == EntryOffer && now.Sub(e.At) <= keep {
			kept[e.ID] = true
		}
	}
	for _, e := range entries {
		switch {
		case e.Type == EntryOffer && kept[e.ID]:
		case e.Type == EntryResolve && kept[e.ID]:
		case e.Type == EntrySeen && kept[e.ID]:
		case e.Type == EntryFeedback && kept[e.ID]:
		case (e.Type == EntryLowered || e.Type == EntryHoldout) && now.Sub(e.At) <= keep:
		default:
			continue
		}
		out = append(out, e)
	}
	return out
}
