package followthrough

import (
	"testing"
	"time"
)

func tip(id, session, kind string, at time.Time) Entry {
	return Entry{Type: EntryOffer, ID: id, At: at, Session: session, Power: "inform", Channel: ChannelTip, Kind: kind}
}

func answer(id string, o Outcome, at time.Time, until time.Time) Entry {
	return Entry{Type: EntryFeedback, ID: id, At: at, Outcome: o, Until: until}
}

func TestLifecycleState(t *testing.T) {
	later := t0.Add(time.Hour)
	cases := []struct {
		name    string
		entries []Entry
		want    State
		outcome Outcome
	}{
		{"offered and not shown is new", []Entry{tip("a", "s", "budget_50", t0)}, StateNew, OutcomeOpen},
		{"shown on the status line is seen", []Entry{tip("a", "s", "budget_50", t0), seen("a", t0)}, StateSeen, OutcomeOpen},
		{"acknowledged", []Entry{tip("a", "s", "budget_50", t0), answer("a", OutcomeAcknowledged, t0, time.Time{})}, StateAcknowledged, OutcomeAcknowledged},
		{"expected", []Entry{tip("a", "s", "budget_50", t0), answer("a", OutcomeExpected, t0, time.Time{})}, StateExpected, OutcomeExpected},
		{"snoozed", []Entry{tip("a", "s", "budget_50", t0), answer("a", OutcomeSnoozed, t0, t0.Add(4*time.Hour))}, StateSnoozed, OutcomeSnoozed},
		{"the newest answer wins", []Entry{
			tip("a", "s", "budget_50", t0),
			answer("a", OutcomeSnoozed, t0, t0.Add(4*time.Hour)),
			answer("a", OutcomeExpected, t0.Add(time.Minute), time.Time{}),
		}, StateExpected, OutcomeExpected},
		{"an answer outranks ignored: the operator did respond", []Entry{
			tip("a", "s", "budget_50", t0), seen("a", t0),
			resolve("a", OutcomeIgnored, t0.Add(time.Minute)),
			answer("a", OutcomeExpected, t0.Add(2*time.Minute), time.Time{}),
		}, StateExpected, OutcomeExpected},
		{"acting on it still counts as followed", []Entry{
			tip("a", "s", "budget_50", t0),
			resolve("a", OutcomeFollowed, t0.Add(time.Minute)),
			answer("a", OutcomeAcknowledged, t0.Add(2*time.Minute), time.Time{}),
		}, StateAcknowledged, OutcomeFollowed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Fold(c.entries, later)
			if len(got) != 1 || got[0].State() != c.want || got[0].Outcome != c.outcome {
				t.Fatalf("got %+v state=%s; want %s/%s", got, got[0].State(), c.want, c.outcome)
			}
		})
	}
}

func TestSummaryCountsAnswers(t *testing.T) {
	es := []Entry{
		tip("a", "s1", "budget_50", t0), answer("a", OutcomeExpected, t0, time.Time{}),
		tip("b", "s2", "budget_50", t0), answer("b", OutcomeSnoozed, t0, t0.Add(time.Hour)),
		tip("c", "s3", "budget_50", t0), answer("c", OutcomeAcknowledged, t0, time.Time{}),
		tip("d", "s4", "budget_50", t0), answer("d", OutcomeSnoozed, t0, t0.Add(time.Hour)),
	}
	s := Summarize(Fold(es, t0.Add(48*time.Hour)), t0.Add(48*time.Hour))
	if len(s) != 1 || s[0].Expected != 1 || s[0].Snoozed != 2 || s[0].Acknowledged != 1 {
		t.Fatalf("summary = %+v", s)
	}
	if got := s[0].SnoozeRate(); got != 0.5 {
		t.Errorf("snooze rate = %v, want 0.5", got)
	}
}

func TestHeldBy(t *testing.T) {
	now := t0.Add(time.Hour)
	cases := []struct {
		name    string
		entries []Entry
		session string
		kind    string
		want    string
	}{
		{"nothing answered holds nothing", []Entry{tip("a", "s", "budget_50", t0)}, "s", "budget_75", ""},
		{"a snooze holds the whole family, in any session", []Entry{
			tip("a", "s", "budget_50", t0), answer("a", OutcomeSnoozed, t0, t0.Add(2*time.Hour)),
		}, "other", "budget_over", HeldSnoozed},
		{"a snooze ends", []Entry{
			tip("a", "s", "budget_50", t0), answer("a", OutcomeSnoozed, t0, t0.Add(30*time.Minute)),
		}, "s", "budget_75", ""},
		{"a snooze holds only its family", []Entry{
			tip("a", "s", "budget_50", t0), answer("a", OutcomeSnoozed, t0, t0.Add(2*time.Hour)),
		}, "s", "compact_now", ""},
		{"an acknowledged budget tip closes the session's budget episode", []Entry{
			tip("a", "s", "budget_75", t0), answer("a", OutcomeAcknowledged, t0, time.Time{}),
		}, "s", "budget_over", HeldEpisode},
		{"expected closes it too", []Entry{
			tip("a", "s", "budget_50", t0), answer("a", OutcomeExpected, t0, time.Time{}),
		}, "s", "budget_75", HeldEpisode},
		{"another session's episode is its own", []Entry{
			tip("a", "s", "budget_50", t0), answer("a", OutcomeExpected, t0, time.Time{}),
		}, "t", "budget_75", ""},
		{"a window about to run out always speaks", []Entry{
			tip("a", "s", "quota_75", t0), answer("a", OutcomeSnoozed, t0, t0.Add(2*time.Hour)),
		}, "s", "quota_90", ""},
		{"a run-out warning always speaks", []Entry{
			tip("a", "s", "quota_burn", t0), answer("a", OutcomeSnoozed, t0, t0.Add(2*time.Hour)),
		}, "s", "quota_burn", ""},
		{"an acknowledged quota tip quiets the rest of its tiers below 90%", []Entry{
			tip("a", "s", "quota_50", t0), answer("a", OutcomeAcknowledged, t0, time.Time{}),
		}, "s", "quota_75", HeldEpisode},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HeldBy(Fold(c.entries, now), c.session, c.kind, now); got != c.want {
				t.Fatalf("HeldBy = %q, want %q", got, c.want)
			}
		})
	}
}

func TestExpectedShare(t *testing.T) {
	es := []Entry{
		tip("a", "s1", "budget_50", t0), answer("a", OutcomeExpected, t0, time.Time{}),
		tip("b", "s2", "budget_over", t0), answer("b", OutcomeExpected, t0, time.Time{}),
		tip("c", "s3", "budget_75", t0), answer("c", OutcomeAcknowledged, t0, time.Time{}),
		tip("d", "s4", "budget_75", t0),
		tip("e", "s5", "quota_75", t0), answer("e", OutcomeExpected, t0, time.Time{}),
	}
	expected, answered := Answers(Fold(es, t0.Add(time.Hour)), "budget")
	if expected != 2 || answered != 3 {
		t.Fatalf("Answers = %d/%d, want 2/3", expected, answered)
	}
}

func TestPruneKeepsFeedback(t *testing.T) {
	es := []Entry{tip("a", "s", "budget_50", t0), answer("a", OutcomeExpected, t0, time.Time{})}
	if got := Prune(es, t0.Add(time.Hour), 24*time.Hour); len(got) != 2 {
		t.Fatalf("Prune dropped the answer: %+v", got)
	}
}
