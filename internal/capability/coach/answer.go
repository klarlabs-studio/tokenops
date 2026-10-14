package coach

import (
	"errors"
	"fmt"
	"time"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

// The answers an operator can give a tip.
const (
	AnswerAcknowledged = string(ft.OutcomeAcknowledged)
	AnswerExpected     = string(ft.OutcomeExpected)
	AnswerSnoozed      = string(ft.OutcomeSnoozed)
)

// DefaultSnooze is how long a snooze lasts when no duration is given.
const DefaultSnooze = 4 * time.Hour

// answerLookback bounds how far back an answer counts as evidence for
// the session budget, the same 30 days the budget is read off.
const answerLookback = 30 * 24 * time.Hour

// ErrNoTip is returned when there is no tip to answer.
var ErrNoTip = errors.New("no tip to answer")

// TipAnswer is a tip as it stands after the operator answered it.
type TipAnswer struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Session string `json:"session,omitempty"`
	// Answer is acknowledged, expected or snoozed.
	Answer string `json:"answer"`
	// SnoozedUntil is when a snooze ends, absent otherwise.
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`
}

// AnswerTip records the operator's answer to a tip: acknowledged,
// expected or snoozed (for d, DefaultSnooze when zero). An empty id
// answers the newest tip not yet answered. The CLI and the MCP tool both
// answer through it.
func AnswerTip(l Ledger, now time.Time, id, answer string, d time.Duration) (TipAnswer, error) {
	o := ft.Outcome(answer)
	if !ft.Answerable(o) {
		return TipAnswer{}, fmt.Errorf("%q is not an answer: use acknowledged, expected or snoozed", answer)
	}
	if l == nil {
		return TipAnswer{}, ErrNoTip
	}
	entries, err := l.Load()
	if err != nil {
		return TipAnswer{}, err
	}
	target, ok := findTip(ft.Fold(entries, now), id)
	if !ok {
		if id != "" {
			return TipAnswer{}, fmt.Errorf("no tip with id %q", id)
		}
		return TipAnswer{}, ErrNoTip
	}
	e := ft.Entry{Type: ft.EntryFeedback, ID: target.Offer.ID, At: now.UTC(), Kind: target.Offer.Kind, Outcome: o}
	out := TipAnswer{ID: target.Offer.ID, Kind: target.Offer.Kind, Session: target.Offer.Session, Answer: answer}
	if o == ft.OutcomeSnoozed {
		if d <= 0 {
			d = DefaultSnooze
		}
		e.Until = now.Add(d).UTC()
		out.SnoozedUntil = &e.Until
	}
	if err := l.Append(e); err != nil {
		return TipAnswer{}, err
	}
	return out, nil
}

// findTip is the tip named by id, or with no id the newest tip not yet
// answered.
func findTip(records []ft.Record, id string) (ft.Record, bool) {
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Offer.Channel != ft.ChannelTip {
			continue
		}
		if id != "" && r.Offer.ID == id {
			return r, true
		}
		if id == "" && r.Answer == "" {
			return r, true
		}
	}
	return ft.Record{}, false
}

// Holder returns why a tip of a kind must not be said in a session
// because of an earlier answer ("snoozed", "answered"), "" when nothing
// holds it. The ledger is read once, on first use. A ledger that cannot
// be read holds nothing: missing evidence must not silence the coach.
func Holder(l Ledger, now time.Time) func(session, kind string) string {
	var records []ft.Record
	loaded := false
	return func(session, kind string) string {
		if !loaded {
			loaded = true
			if l != nil {
				if entries, err := l.Load(); err == nil {
					records = ft.Fold(entries, now)
				}
			}
		}
		return ft.HeldBy(records, session, kind, now)
	}
}

// BudgetAnswers counts the session-budget tips the operator answered in
// the last 30 days, and how many of those they called expected: the
// evidence that tunes the budget belief.
func BudgetAnswers(l Ledger, now time.Time) (expected, answered int) {
	if l == nil {
		return 0, 0
	}
	entries, err := l.Load()
	if err != nil {
		return 0, 0
	}
	var recent []ft.Record
	for _, r := range ft.Fold(entries, now) {
		if now.Sub(r.Offer.At) <= answerLookback {
			recent = append(recent, r)
		}
	}
	return ft.Answers(recent, "budget")
}
