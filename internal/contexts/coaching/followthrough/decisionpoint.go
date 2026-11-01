package followthrough

import (
	"fmt"
	"time"
)

// Decision points are where the coach speaks to the agent at the moment a
// choice is made, rather than in a line after the turn. A nudge that names
// one object at the decision it bears on is the kind that gets acted on;
// one said afterwards, about the session in general, mostly is not.
const (
	// PointToolCall is a tool call the read guard sees: a re-read or a
	// repeated search (the loop tips).
	PointToolCall = "tool_call"
	// PointPrepareWork is the agent asking tokenops_prepare_work before
	// it starts a task.
	PointPrepareWork = "prepare_work"
	// PointSubagent is the agent launching a subagent.
	PointSubagent = "subagent_launch"
	// PointModelChoice is the per-turn model advice, given as the
	// operator's prompt opens a turn.
	PointModelChoice = "model_choice"
)

// The decision-point throttle's defaults. A tip at every decision would
// be noise however apt each one is, so the coach says only a few an hour
// in any one session, and never the same thing twice in a row.
const (
	// DefaultTipsPerHour is how many decision-point tips one session may
	// be given in any hour.
	DefaultTipsPerHour = 6
	// DefaultTipCooldown is how long the same tip (kind and object) waits
	// before it may be given again in the same session.
	DefaultTipCooldown = time.Hour
)

// Why Admit holds a tip back.
const (
	// HeldHourly: the session has had its tips for the hour.
	HeldHourly = "hourly"
	// HeldCooldown: the same tip was given within the cool-down.
	HeldCooldown = "cooldown"
)

// Throttle bounds how often the coach speaks at decision points.
type Throttle struct {
	// PerHour is the most tips one session is given in any hour; zero or
	// less is DefaultTipsPerHour.
	PerHour int
	// Cooldown is how long the same kind and object wait to be given
	// again in a session; zero or less is DefaultTipCooldown.
	Cooldown time.Duration
}

func (t Throttle) perHour() int {
	if t.PerHour <= 0 {
		return DefaultTipsPerHour
	}
	return t.PerHour
}

func (t Throttle) cooldown() time.Duration {
	if t.Cooldown <= 0 {
		return DefaultTipCooldown
	}
	return t.Cooldown
}

// Admit reports why a decision-point tip of kind naming object must not be
// given in session now, or "" when it may be. It counts the offers in the
// ledger made at a decision point: the hourly budget is per session, and
// tips given without a session (an MCP tool call) share one budget.
func (t Throttle) Admit(entries []Entry, session, kind, object string, now time.Time) string {
	given := 0
	for _, e := range entries {
		if e.Type != EntryOffer || e.Point == "" || e.Session != session {
			continue
		}
		age := now.Sub(e.At)
		if age < 0 {
			continue
		}
		if e.Kind == kind && e.Object == object && age < t.cooldown() {
			return HeldCooldown
		}
		if age < time.Hour {
			given++
		}
	}
	if given >= t.perHour() {
		return HeldHourly
	}
	return ""
}

// The retirement rule's thresholds. Quieting (Quieted) reacts to a run of
// ignored offers and lapses after two weeks; retirement is the long-run
// verdict on a kind of tip that is seen and passed over most of the time.
const (
	// RetireMinSeen is how many seen offers with an outcome a kind needs
	// before it can be retired: under it a rate is noise.
	RetireMinSeen = 20
	// RetireIgnoredShare is the share of those that were ignored at or
	// above which the kind is retired.
	RetireIgnoredShare = 0.8
	// RetireLookback bounds the evidence.
	RetireLookback = 30 * 24 * time.Hour
)

// EntryReinstated is the operator bringing back a retired kind. Evidence
// before it no longer counts toward retiring the kind again.
const EntryReinstated EntryType = "reinstated"

// Retirement is a kind the coach stopped offering because, when it was
// seen, it was ignored.
type Retirement struct {
	Power string `json:"power"`
	Kind  string `json:"kind"`
	// Seen is how many offers in the look-back were seen and settled,
	// and Ignored how many of those were ignored.
	Seen    int `json:"seen"`
	Ignored int `json:"ignored"`
}

// Why says, in a line, what the retirement rests on.
func (r Retirement) Why() string {
	return fmt.Sprintf("ignored %d of the %d times it was seen in the last %d days",
		r.Ignored, r.Seen, int(RetireLookback/(24*time.Hour)))
}

// Reinstatements is when each power and kind was last reinstated.
func Reinstatements(entries []Entry) map[string]time.Time {
	out := map[string]time.Time{}
	for _, e := range entries {
		if e.Type != EntryReinstated {
			continue
		}
		k := e.Power + "\x00" + e.Kind
		if e.At.After(out[k]) {
			out[k] = e.At
		}
	}
	return out
}

// Retired reports whether tips or advice of power and kind are retired:
// over RetireLookback, and since the kind was last reinstated, at least
// RetireMinSeen offers were seen and settled and RetireIgnoredShare of
// them were ignored.
//
// Only evidence of being seen counts. A tip nothing shows was read is
// unseen, not ignored, and an unseen tip never retires anything. Advice is
// answered in the agent's own turn and counts as seen. Tips about work
// about to stop are never retired.
func Retired(records []Record, reinstated map[string]time.Time, power, kind string, now time.Time) (Retirement, bool) {
	r := Retirement{Power: power, Kind: kind}
	if alwaysSpeaks(kind) {
		return r, false
	}
	since := reinstated[power+"\x00"+kind]
	for _, rec := range records {
		o := rec.Offer
		if o.Power != power || o.Kind != kind || (o.Channel != ChannelTip && o.Channel != ChannelAdvice) {
			continue
		}
		if !o.At.After(since) || now.Sub(o.At) > RetireLookback {
			continue
		}
		if o.Channel == ChannelTip && !rec.Seen {
			continue
		}
		switch rec.Outcome {
		case OutcomeFollowed:
			r.Seen++
		case OutcomeIgnored:
			r.Seen++
			r.Ignored++
		}
	}
	if r.Seen < RetireMinSeen {
		return r, false
	}
	return r, float64(r.Ignored)/float64(r.Seen) >= RetireIgnoredShare
}

// SummarizeLedger folds the ledger and summarizes it, marking the kinds
// that are retired.
func SummarizeLedger(entries []Entry, now time.Time) []Summary {
	records := Fold(entries, now)
	sums := Summarize(records, now)
	back := Reinstatements(entries)
	for i := range sums {
		if since, ok := back[sums[i].Power+"\x00"+sums[i].Kind]; ok && sums[i].Quiet {
			sums[i].Quiet = Quieted(after(records, since), sums[i].Power, sums[i].Kind, now)
		}
		if sums[i].Channel != ChannelTip && sums[i].Channel != ChannelAdvice {
			continue
		}
		if r, ok := Retired(records, back, sums[i].Power, sums[i].Kind, now); ok {
			sums[i].Retired = &r
		}
	}
	return sums
}

// Silenced reports whether the coach holds back power and kind: quieted
// by a run of ignored offers, or retired. A reinstatement clears both:
// only offers after it count.
func Silenced(entries []Entry, now time.Time) func(power, kind string) bool {
	records := Fold(entries, now)
	back := Reinstatements(entries)
	return func(power, kind string) bool {
		rs := records
		if since, ok := back[power+"\x00"+kind]; ok {
			rs = after(records, since)
		}
		if Quieted(rs, power, kind, now) {
			return true
		}
		_, retired := Retired(records, back, power, kind, now)
		return retired
	}
}

// after is the records offered after since.
func after(records []Record, since time.Time) []Record {
	out := make([]Record, 0, len(records))
	for _, r := range records {
		if r.Offer.At.After(since) {
			out = append(out, r)
		}
	}
	return out
}
