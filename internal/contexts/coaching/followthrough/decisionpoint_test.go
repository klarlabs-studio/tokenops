package followthrough

import (
	"fmt"
	"testing"
	"time"
)

func pointOffer(id, session, kind, object string, at time.Time) Entry {
	return Entry{Type: EntryOffer, ID: id, At: at, Session: session, Power: "inform",
		Channel: ChannelTip, Kind: kind, Point: PointToolCall, Object: object, Via: ViaAgent}
}

func TestThrottleAdmit(t *testing.T) {
	now := t0.Add(10 * time.Hour)
	many := func(n int, session string, age time.Duration) []Entry {
		var out []Entry
		for i := range n {
			out = append(out, pointOffer(fmt.Sprint(i), session, "reread_churn", fmt.Sprintf("f%d.go", i), now.Add(-age)))
		}
		return out
	}
	cases := []struct {
		name     string
		throttle Throttle
		entries  []Entry
		session  string
		kind     string
		object   string
		want     string
	}{
		{"an empty ledger admits", Throttle{}, nil, "s", "reread_churn", "a.go", ""},
		{"the hour's budget spent holds", Throttle{}, many(DefaultTipsPerHour, "s", 10*time.Minute), "s", "search_loop", "Grep", HeldHourly},
		{"one under the budget admits", Throttle{}, many(DefaultTipsPerHour-1, "s", 10*time.Minute), "s", "search_loop", "Grep", ""},
		{"tips over an hour old do not count", Throttle{}, many(DefaultTipsPerHour, "s", 61*time.Minute), "s", "search_loop", "Grep", ""},
		{"another session's tips do not count", Throttle{}, many(DefaultTipsPerHour, "other", time.Minute), "s", "search_loop", "Grep", ""},
		{"a configured budget is used", Throttle{PerHour: 2}, many(2, "s", time.Minute), "s", "search_loop", "Grep", HeldHourly},
		{"the same tip in the cool-down holds", Throttle{}, []Entry{pointOffer("a", "s", "reread_churn", "a.go", now.Add(-30*time.Minute))}, "s", "reread_churn", "a.go", HeldCooldown},
		{"the same kind on another object admits", Throttle{}, []Entry{pointOffer("a", "s", "reread_churn", "a.go", now.Add(-30*time.Minute))}, "s", "reread_churn", "b.go", ""},
		{"the same tip past the cool-down admits", Throttle{}, []Entry{pointOffer("a", "s", "reread_churn", "a.go", now.Add(-DefaultTipCooldown))}, "s", "reread_churn", "a.go", ""},
		{"a configured cool-down is used", Throttle{Cooldown: 3 * time.Hour}, []Entry{pointOffer("a", "s", "reread_churn", "a.go", now.Add(-2*time.Hour))}, "s", "reread_churn", "a.go", HeldCooldown},
		{"tips after the turn do not count", Throttle{PerHour: 1}, []Entry{{Type: EntryOffer, ID: "x", At: now.Add(-time.Minute), Session: "s", Channel: ChannelTip, Kind: "quota_90"}}, "s", "reread_churn", "a.go", ""},
		{"session-less tips share one budget", Throttle{PerHour: 1}, []Entry{pointOffer("a", "", "window_fit", "5h window", now.Add(-time.Minute))}, "", "model_fit", "haiku", HeldHourly},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.throttle.Admit(c.entries, c.session, c.kind, c.object, now); got != c.want {
				t.Fatalf("Admit = %q; want %q", got, c.want)
			}
		})
	}
}

// settled is n offers of kind, the first ignored of them ignored, each
// seen (when seen) and resolved.
func settled(kind string, n, ignored int, seenIt bool, start time.Time) []Entry {
	var out []Entry
	for i := range n {
		id := fmt.Sprintf("%s-%d", kind, i)
		at := start.Add(time.Duration(i) * time.Minute)
		out = append(out, pointOffer(id, "s", kind, "a.go", at))
		if seenIt {
			out = append(out, Entry{Type: EntrySeen, ID: id, At: at.Add(time.Second), Via: ViaAgent})
		}
		o := OutcomeFollowed
		if i < ignored {
			o = OutcomeIgnored
		}
		out = append(out, Entry{Type: EntryResolve, ID: id, At: at.Add(time.Second), Outcome: o, Instead: "read a.go again"})
	}
	return out
}

func TestRetired(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	start := t0
	cases := []struct {
		name    string
		entries []Entry
		kind    string
		want    bool
	}{
		{"seen and ignored at 80% over 20 retires", settled("reread_churn", 20, 16, true, start), "reread_churn", true},
		{"under 80% ignored stays", settled("reread_churn", 20, 15, true, start), "reread_churn", false},
		{"under 20 seen stays", settled("reread_churn", 19, 19, true, start), "reread_churn", false},
		{"unseen tips never retire", settled("reread_churn", 40, 40, false, start), "reread_churn", false},
		{"tips about work about to stop never retire", settled("quota_90", 30, 30, true, start), "quota_90", false},
		{"evidence older than the look-back does not count", settled("reread_churn", 20, 20, true, now.Add(-RetireLookback-time.Hour)), "reread_churn", false},
		{"a reinstatement wipes the evidence before it", append(settled("reread_churn", 20, 20, true, start),
			Entry{Type: EntryReinstated, At: start.Add(time.Hour), Power: "inform", Kind: "reread_churn"}), "reread_churn", false},
		{"a reinstatement of another kind does not", append(settled("reread_churn", 20, 20, true, start),
			Entry{Type: EntryReinstated, At: start.Add(time.Hour), Power: "inform", Kind: "search_loop"}), "reread_churn", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, got := Retired(Fold(c.entries, now), Reinstatements(c.entries), "inform", c.kind, now)
			if got != c.want {
				t.Fatalf("Retired = %v (%+v); want %v", got, r, c.want)
			}
			if got && r.Why() == "" {
				t.Fatal("a retirement says why")
			}
		})
	}
}

func TestSummarizeLedgerMarksRetiredAndOverrideContext(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	entries := settled("reread_churn", 20, 18, true, t0)
	sums := SummarizeLedger(entries, now)
	if len(sums) != 1 {
		t.Fatalf("sums = %+v", sums)
	}
	s := sums[0]
	if s.Retired == nil || s.Retired.Seen != 20 || s.Retired.Ignored != 18 {
		t.Fatalf("Retired = %+v; want 18 of 20", s.Retired)
	}
	if s.LastPoint != PointToolCall || s.LastInstead != "read a.go again" {
		t.Fatalf("override context = %q / %q", s.LastPoint, s.LastInstead)
	}
	if !Silenced(entries, now)("inform", "reread_churn") {
		t.Fatal("a retired kind is silenced")
	}
	back := append(entries, Entry{Type: EntryReinstated, At: now.Add(-time.Minute), Power: "inform", Kind: "reread_churn"})
	if Silenced(back, now)("inform", "reread_churn") {
		t.Fatal("a reinstated kind speaks again")
	}
}

func TestPruneKeepsReinstatements(t *testing.T) {
	now := t0
	entries := []Entry{
		{Type: EntryReinstated, At: now.Add(-time.Hour), Power: "inform", Kind: "reread_churn"},
		{Type: EntryReinstated, At: now.Add(-100 * 24 * time.Hour), Power: "inform", Kind: "search_loop"},
	}
	got := Prune(entries, now, 90*24*time.Hour)
	if len(got) != 1 || got[0].Kind != "reread_churn" {
		t.Fatalf("Prune = %+v", got)
	}
}
