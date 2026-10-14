package followthrough

import (
	"strconv"
	"strings"
	"time"
)

// The operator's answers to a tip. They are outcomes like followed and
// ignored, but given rather than inferred: the cheapest labelled
// evidence the coach gets.
const (
	// OutcomeAcknowledged is "I've seen it": nothing more about this
	// episode, please.
	OutcomeAcknowledged Outcome = "acknowledged"
	// OutcomeExpected is "this is normal for me": the tip was accurate
	// and not a problem. It is what tunes the bars the tips measure
	// against.
	OutcomeExpected Outcome = "expected"
	// OutcomeSnoozed is "not now": tips of the family wait until Until.
	OutcomeSnoozed Outcome = "snoozed"
)

// EntryFeedback is the operator answering a tip: acknowledged, expected
// or snoozed. It carries the offer's ID; a snooze also carries Until.
const EntryFeedback EntryType = "feedback"

// State is where a tip is in its lifecycle: new → seen → acknowledged,
// expected or snoozed.
type State string

// The states.
const (
	StateNew          State = "new"
	StateSeen         State = "seen"
	StateAcknowledged State = "acknowledged"
	StateExpected     State = "expected"
	StateSnoozed      State = "snoozed"
)

// Answerable reports whether o is an answer an operator may give.
func Answerable(o Outcome) bool {
	return o == OutcomeAcknowledged || o == OutcomeExpected || o == OutcomeSnoozed
}

// State is the record's place in the tip lifecycle.
func (r Record) State() State {
	switch r.Answer {
	case OutcomeAcknowledged:
		return StateAcknowledged
	case OutcomeExpected:
		return StateExpected
	case OutcomeSnoozed:
		return StateSnoozed
	}
	if r.Seen {
		return StateSeen
	}
	return StateNew
}

// Family groups tip kinds that are one subject at different strengths:
// "budget" for budget_50 … budget_over, "quota" for quota_50 …
// quota_100, "compact" for compact_now.
func Family(kind string) string {
	f, _, _ := strings.Cut(kind, "_")
	return f
}

// alwaysSpeaks reports a tip about work about to stop: a plan window
// forecast to run out before it resets (quota_burn) or at 90% or more
// (quota_90, quota_100). No answer holds it back, because acting on it
// is the only way to keep working. Only the share tips earlier versions
// gave below 90% (quota_50, quota_75) can be held.
func alwaysSpeaks(kind string) bool {
	if Family(kind) != "quota" {
		return false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(kind, "quota_"))
	return err != nil || n >= 90
}

// Why HeldBy holds a tip back.
const (
	// HeldSnoozed: a tip of the family was snoozed and the snooze runs.
	HeldSnoozed = "snoozed"
	// HeldEpisode: a tip of the family was acknowledged or expected in
	// this session, which closes the episode: each alert is said once
	// per episode, not again at every step.
	HeldEpisode = "answered"
)

// HeldBy reports why a tip of kind must not be said in session now
// because of an earlier answer, or "" when nothing holds it.
func HeldBy(records []Record, session, kind string, now time.Time) string {
	if alwaysSpeaks(kind) {
		return ""
	}
	fam := Family(kind)
	held := ""
	for _, r := range records {
		if r.Offer.Channel != ChannelTip || Family(r.Offer.Kind) != fam {
			continue
		}
		switch r.Answer {
		case OutcomeSnoozed:
			if now.Before(r.SnoozedUntil) {
				return HeldSnoozed
			}
		case OutcomeAcknowledged, OutcomeExpected:
			if session != "" && r.Offer.Session == session {
				held = HeldEpisode
			}
		}
	}
	return held
}

// Answers counts, among tips of a family, how many the operator
// answered and how many of those answers were "expected".
func Answers(records []Record, family string) (expected, answered int) {
	for _, r := range records {
		if r.Offer.Channel != ChannelTip || Family(r.Offer.Kind) != family || !Answerable(r.Answer) {
			continue
		}
		answered++
		if r.Answer == OutcomeExpected {
			expected++
		}
	}
	return expected, answered
}

// SnoozeRate is the share of the kind's offers that were snoozed. A coach
// whose tips are mostly snoozed is noise with a polite face, and this is
// where that shows.
func (s Summary) SnoozeRate() float64 {
	total := s.Followed + s.Ignored + s.Open + s.Unknown + s.Unseen + s.Acknowledged + s.Expected + s.Snoozed
	if total == 0 {
		return 0
	}
	return float64(s.Snoozed) / float64(total)
}
