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
)

// Outcome is what became of one intervention.
type Outcome string

// The outcomes. Followed and Ignored are recorded; Stood, Undone, Open, and
// Unknown are derived when the ledger is folded.
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
)

// Entry is one ledger line.
type Entry struct {
	Type EntryType `json:"type"`
	// ID names an offer; a resolve carries the ID of the offer it closes.
	ID      string    `json:"id,omitempty"`
	At      time.Time `json:"at"`
	Session string    `json:"session,omitempty"`
	// Power is inform, waste, or models.
	Power   string  `json:"power"`
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
}

// UndoWindow is how long after an autonomous move lowering the power
// still counts as undoing it. A move that outlives it stood.
const UndoWindow = 24 * time.Hour

// OpenFor is how long an advice or tip offer without a recorded outcome
// stays open. Past it the session has ended, and the outcome is unknown.
const OpenFor = 24 * time.Hour

// Record is one offer with its outcome.
type Record struct {
	Offer      Entry     `json:"offer"`
	Outcome    Outcome   `json:"outcome"`
	ResolvedAt time.Time `json:"resolved_at,omitzero"`
	Evidence   string    `json:"evidence,omitempty"`
}

// Fold turns the ledger into one record per offer, oldest first.
func Fold(entries []Entry, now time.Time) []Record {
	resolved := map[string]Entry{}
	var lowered []Entry
	for _, e := range entries {
		switch e.Type {
		case EntryResolve:
			if _, seen := resolved[e.ID]; !seen {
				resolved[e.ID] = e
			}
		case EntryLowered:
			lowered = append(lowered, e)
		}
	}
	var out []Record
	for _, e := range entries {
		if e.Type != EntryOffer {
			continue
		}
		r := Record{Offer: e}
		res, ok := resolved[e.ID]
		switch {
		case ok:
			r.Outcome, r.ResolvedAt, r.Evidence = res.Outcome, res.At, res.Evidence
		case e.Channel == ChannelMove:
			r.Outcome, r.ResolvedAt, r.Evidence = moveOutcome(e, lowered, now)
		case now.Sub(e.At) < OpenFor:
			r.Outcome = OutcomeOpen
		default:
			r.Outcome = OutcomeUnknown
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
		}
	}
	for i := range out {
		out[i].Quiet = out[i].Channel == ChannelAdvice && Quieted(records, out[i].Power, out[i].Kind, now)
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

// Quieted reports whether advice of this power and kind has been ignored
// QuietAfter times in a row within QuietLookback, with nothing followed
// since. Offers without a known outcome do not count either way.
func Quieted(records []Record, power, kind string, now time.Time) bool {
	ignored := 0
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Offer.Channel != ChannelAdvice || r.Offer.Power != power || r.Offer.Kind != kind {
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
		case e.Type == EntryLowered && now.Sub(e.At) <= keep:
		default:
			continue
		}
		out = append(out, e)
	}
	return out
}
