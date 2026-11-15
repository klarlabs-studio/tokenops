package coach

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/contexts/learning"
)

// At the ask rung the context power proposes the compaction line it
// measured from the developer's own instructions (ADR 0021) and writes it
// only on their yes. A proposal is a follow-through approval offer, the
// same record the models power keeps when it asks before a move; the yes
// or no is the offer's resolve. Until the developer accepts, and while the
// compaction guardrail has not graduated, what would be set is recorded as
// a shadow in the savings ledger and nothing is written.

// KindCompactLine is the kind of a compaction-line proposal.
const KindCompactLine = savings.KindCompactLine

// AcceptCommand is the one command that accepts the newest proposal.
const AcceptCommand = "tokenops coach accept"

// ErrNoProposal is returned when there is no compaction proposal to answer.
var ErrNoProposal = errors.New("no compaction proposal to answer")

// AskEnv is what the ask rung reads and writes.
type AskEnv struct {
	// Ledger holds the proposals and their answers.
	Ledger Ledger
	// Shadow is the savings ledger the would-be settings are recorded in.
	Shadow savings.Ledger
	Levers ContextLevers
	// Graduated reports whether the compaction guardrail's evidence clears
	// the bar to act; nil reads as not graduated.
	Graduated func() bool
}

func (e AskEnv) graduated() bool { return e.Graduated != nil && e.Graduated() }

// CompactProposal is a compaction line put to the developer.
type CompactProposal struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
	// Settings is each agent's setting and the value it would take.
	Settings string `json:"settings"`
	// Command accepts it.
	Command string `json:"command"`
}

// CompactAnswer is a proposal after the developer answered it.
type CompactAnswer struct {
	CompactProposal
	// Accepted is true for a yes, false for a no.
	Accepted bool `json:"accepted"`
	// Written is true when the settings were written on the yes; false
	// when they wait on the compaction guardrail's graduation (Note says
	// so) or the answer was no.
	Written bool          `json:"written"`
	Note    string        `json:"note,omitempty"`
	Results []LeverResult `json:"results,omitempty"`
}

// StepCompaction is the context power's step at the ask rung, run after
// the daily waste report has measured the context line: it proposes the
// line when it differs from what is set and has not been put already,
// writes an accepted line once the guardrail has graduated, and otherwise
// records what it would have set as a shadow. At any other rung it does
// nothing.
func StepCompaction(cfg config.Config, env AskEnv, now time.Time) []LeverResult {
	if env.Levers == nil || Build(cfg).Effective(config.PowerContext) != config.AutonomyAsk {
		return nil
	}
	would, err := wouldSet(env.Levers)
	if err != nil || len(would) == 0 {
		return withError(nil, err)
	}
	to := describeSettings(would)
	records := foldedProposals(env.Ledger, now)
	if accepted(records, to) && env.graduated() {
		rs, err := env.Levers.Apply()
		return withError(rs, err)
	}
	savings.RecordCompactionShadow(env.Shadow, now, NewID(), to)
	if !answeredOrPending(records, to) {
		propose(env.Ledger, now, to)
	}
	return would
}

// AcceptCompaction records the developer's yes to the proposal id (the
// newest unanswered one when empty) and writes the settings when the
// compaction guardrail has graduated; otherwise it records them as a
// shadow and they are written once it graduates.
func AcceptCompaction(env AskEnv, id string, now time.Time) (CompactAnswer, error) {
	target, err := openProposal(env.Ledger, id, now)
	if err != nil {
		return CompactAnswer{}, err
	}
	if env.Levers == nil {
		return CompactAnswer{}, errors.New("the agents' settings cannot be found here")
	}
	would, err := wouldSet(env.Levers)
	if err != nil {
		return CompactAnswer{}, err
	}
	if len(would) > 0 && describeSettings(would) != target.Settings {
		return CompactAnswer{}, fmt.Errorf("the proposal is out of date: the line has moved to %s since; it is proposed again after the next waste report", describeSettings(would))
	}
	if err := resolve(env.Ledger, target.ID, now, ft.OutcomeFollowed, "accepted with "+AcceptCommand); err != nil {
		return CompactAnswer{}, err
	}
	out := CompactAnswer{CompactProposal: target, Accepted: true}
	if !env.graduated() {
		savings.RecordCompactionShadow(env.Shadow, now, NewID(), target.Settings)
		out.Results = would
		out.Note = "accepted; the compaction guardrail has not graduated yet, so tokenops records what it would set and writes it once the guardrail graduates"
		return out, nil
	}
	rs, err := env.Levers.Apply()
	out.Results, out.Written = withError(rs, err), err == nil
	return out, nil
}

// DeclineCompaction records the developer's no to the proposal id (the
// newest unanswered one when empty). The same line is not proposed again;
// a different one is.
func DeclineCompaction(l Ledger, id string, now time.Time) (CompactAnswer, error) {
	target, err := openProposal(l, id, now)
	if err != nil {
		return CompactAnswer{}, err
	}
	if err := resolve(l, target.ID, now, ft.OutcomeIgnored, "declined"); err != nil {
		return CompactAnswer{}, err
	}
	return CompactAnswer{CompactProposal: target, Note: "declined; this line is not proposed again"}, nil
}

// PendingCompaction is the newest proposal the developer has not answered.
func PendingCompaction(l Ledger, now time.Time) (CompactProposal, bool) {
	p, err := openProposal(l, "", now)
	return p, err == nil
}

// CompactionGraduated reports whether the compaction guardrail's evidence
// clears the bar to act (ADR 0005's bar, the same graduation the coach
// reports), read from the savings ledger and the follow-through ledger l.
func CompactionGraduated(l Ledger, now time.Time) bool {
	advise := func(string) string { return config.AutonomyAdvise }
	steps := savings.Graduations(savings.Build(context.Background(), SavingsDeps(), now), advise, History(l, now))
	for _, s := range steps {
		if s.Capability == learning.CapabilityCompaction {
			return s.Ready
		}
	}
	return false
}

// wouldSet is what the levers would write now: the settings a preview
// finds not yet in place.
func wouldSet(levers ContextLevers) ([]LeverResult, error) {
	rs, err := levers.Preview()
	var out []LeverResult
	for _, r := range rs {
		if r.Status == LeverShadow {
			out = append(out, r)
		}
	}
	return out, err
}

// LeverShadow is the status of a setting a preview would write.
const LeverShadow = "shadow"

// describeSettings names each setting and value in a stable order, so the
// same line reads the same way on every step.
func describeSettings(rs []LeverResult) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf("%s %s=%d", r.Client, r.Key, r.Value))
	}
	slices.Sort(parts)
	return strings.Join(parts, "; ")
}

func propose(l Ledger, now time.Time, to string) {
	RecordApprovalBecause(l, now, NewID(), "", config.PowerContext, KindCompactLine, "", to,
		"the context line measured from your own instructions")
}

func resolve(l Ledger, id string, now time.Time, o ft.Outcome, evidence string) error {
	if l == nil {
		return ErrNoProposal
	}
	return l.Append(ft.Entry{Type: ft.EntryResolve, ID: id, At: now.UTC(), Kind: KindCompactLine, Outcome: o, Evidence: evidence})
}

// foldedProposals are the compaction proposals in the ledger, oldest first.
func foldedProposals(l Ledger, now time.Time) []ft.Record {
	if l == nil {
		return nil
	}
	entries, err := l.Load()
	if err != nil {
		return nil
	}
	var out []ft.Record
	for _, r := range ft.Fold(entries, now) {
		if isProposal(r.Offer) {
			out = append(out, r)
		}
	}
	return out
}

func isProposal(e ft.Entry) bool {
	return e.Channel == ft.ChannelApproval && e.Power == config.PowerContext && e.Kind == KindCompactLine
}

// answered reports whether the record has the developer's yes or no. A
// proposal stays open until answered, past ft.OpenFor: it waits on the
// developer, not on a session.
func answered(r ft.Record) bool {
	return r.Outcome == ft.OutcomeFollowed || r.Outcome == ft.OutcomeIgnored
}

// accepted reports whether the developer said yes to the line to.
func accepted(records []ft.Record, to string) bool {
	for i := len(records) - 1; i >= 0; i-- {
		if r := records[i]; r.Offer.To == to && answered(r) {
			return r.Outcome == ft.OutcomeFollowed
		}
	}
	return false
}

// answeredOrPending reports whether the line to has been put already:
// answered either way, or waiting for an answer.
func answeredOrPending(records []ft.Record, to string) bool {
	return slices.ContainsFunc(records, func(r ft.Record) bool { return r.Offer.To == to })
}

// openProposal is the unanswered proposal id names, or with no id the
// newest unanswered one.
func openProposal(l Ledger, id string, now time.Time) (CompactProposal, error) {
	records := foldedProposals(l, now)
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if answered(r) || (id != "" && r.Offer.ID != id) {
			continue
		}
		if id == "" && i < len(records)-1 {
			// A newer proposal was answered or superseded this one.
			break
		}
		return CompactProposal{ID: r.Offer.ID, At: r.Offer.At, Settings: r.Offer.To, Command: AcceptCommand}, nil
	}
	if id != "" {
		return CompactProposal{}, fmt.Errorf("no unanswered compaction proposal with id %q", id)
	}
	return CompactProposal{}, ErrNoProposal
}
