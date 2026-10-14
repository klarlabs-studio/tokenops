package followthrough

import (
	"fmt"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func offer(id string, ch Channel, kind string, at time.Time) Entry {
	return Entry{Type: EntryOffer, ID: id, At: at, Power: "models", Channel: ch, Kind: kind}
}

func resolve(id string, o Outcome, at time.Time) Entry {
	return Entry{Type: EntryResolve, ID: id, At: at, Outcome: o}
}

func TestFoldOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		entries []Entry
		now     time.Time
		want    Outcome
	}{
		{"recorded outcome wins", []Entry{offer("a", ChannelAdvice, "lookup", t0), resolve("a", OutcomeFollowed, t0.Add(time.Minute))}, t0.Add(time.Hour), OutcomeFollowed},
		{"advice inside its window is open", []Entry{offer("a", ChannelAdvice, "lookup", t0)}, t0.Add(time.Hour), OutcomeOpen},
		{"a seen tip whose session ended was followed", []Entry{offer("a", ChannelTip, "quota_75", t0), seen("a", t0)}, t0.Add(OpenFor), OutcomeFollowed},
		{"an unseen tip whose session ended is unseen, not followed", []Entry{offer("a", ChannelTip, "quota_75", t0)}, t0.Add(OpenFor), OutcomeUnseen},
		{"advice whose session ended is unknown, not ignored", []Entry{offer("a", ChannelAdvice, "lookup", t0)}, t0.Add(OpenFor), OutcomeUnknown},
		{"a move inside the undo window is open", []Entry{offer("a", ChannelMove, "lookup", t0)}, t0.Add(time.Hour), OutcomeOpen},
		{"a move that outlives the undo window stood", []Entry{offer("a", ChannelMove, "lookup", t0)}, t0.Add(UndoWindow), OutcomeStood},
		{"lowering the power soon after undoes a move", []Entry{
			offer("a", ChannelMove, "lookup", t0),
			{Type: EntryLowered, At: t0.Add(time.Hour), Power: "models", From: "autonomous", To: "advise"},
		}, t0.Add(48 * time.Hour), OutcomeUndone},
		{"lowering another power does not undo it", []Entry{
			offer("a", ChannelMove, "lookup", t0),
			{Type: EntryLowered, At: t0.Add(time.Hour), Power: "waste", To: "advise"},
		}, t0.Add(48 * time.Hour), OutcomeStood},
		{"lowering before the move does not undo it", []Entry{
			{Type: EntryLowered, At: t0.Add(-time.Hour), Power: "models", To: "advise"},
			offer("a", ChannelMove, "lookup", t0),
		}, t0.Add(48 * time.Hour), OutcomeStood},
		{"lowering after the undo window does not undo it", []Entry{
			offer("a", ChannelMove, "lookup", t0),
			{Type: EntryLowered, At: t0.Add(UndoWindow + time.Hour), Power: "models", To: "advise"},
		}, t0.Add(48 * time.Hour), OutcomeStood},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Fold(c.entries, c.now)
			if len(got) != 1 || got[0].Outcome != c.want {
				t.Fatalf("Fold = %+v; want one record %s", got, c.want)
			}
		})
	}
}

func seen(id string, at time.Time) Entry {
	return Entry{Type: EntrySeen, ID: id, At: at, Via: ViaStatusLine}
}

// A tip delivered only as the Stop hook's systemMessage may never have
// been read: the line is dim, easy to miss, and absent when the session
// is driven remotely. Nothing happening after it is no evidence it was
// turned down, so it must not count as ignored, and must not quiet the
// coach.
func TestTipOutcomesNeedEvidenceTheTipWasSeen(t *testing.T) {
	at := t0.Add(time.Minute)
	cases := []struct {
		name    string
		entries []Entry
		want    Outcome
	}{
		{"unseen tip left alone is unseen", []Entry{offer("a", ChannelTip, "quota_75", t0), resolve("a", OutcomeIgnored, at)}, OutcomeUnseen},
		{"tip seen in the status line and left alone is ignored", []Entry{offer("a", ChannelTip, "quota_75", t0), seen("a", t0), resolve("a", OutcomeIgnored, at)}, OutcomeIgnored},
		{"the seen mark may land after the resolve", []Entry{offer("a", ChannelTip, "quota_75", t0), resolve("a", OutcomeIgnored, at), seen("a", t0)}, OutcomeIgnored},
		{"an unseen tip acted on is still followed", []Entry{offer("a", ChannelTip, "quota_75", t0), resolve("a", OutcomeFollowed, at)}, OutcomeFollowed},
		{"advice is not a tip", []Entry{offer("a", ChannelAdvice, "lookup", t0), resolve("a", OutcomeIgnored, at)}, OutcomeIgnored},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Fold(c.entries, t0.Add(time.Hour))
			if len(got) != 1 || got[0].Outcome != c.want {
				t.Fatalf("Fold = %+v; want one record %s", got, c.want)
			}
		})
	}
}

func TestUnseenTipsNeitherQuietNorCountAsResolved(t *testing.T) {
	es := make([]Entry, 0, 2*(QuietAfter+2))
	for i := range QuietAfter + 2 {
		id := fmt.Sprintf("t-%d", i)
		at := t0.Add(time.Duration(i) * time.Hour)
		es = append(es, Entry{Type: EntryOffer, ID: id, At: at, Power: "inform", Channel: ChannelTip, Kind: "quota_75", Via: ViaStopHook},
			resolve(id, OutcomeIgnored, at.Add(time.Minute)))
	}
	now := t0.Add(10 * time.Hour)
	records := Fold(es, now)
	if Quieted(records, "inform", "quota_75", now) {
		t.Fatal("tips nobody saw quieted the coach")
	}
	s := Summarize(records, now)
	if len(s) != 1 || s[0].Unseen != QuietAfter+2 || s[0].Ignored != 0 || s[0].Resolved() != 0 || s[0].Quiet {
		t.Fatalf("summary = %+v; want every tip unseen", s)
	}
}

func TestPruneKeepsSeenMarksWithTheirOffers(t *testing.T) {
	now := t0.Add(100 * 24 * time.Hour)
	es := []Entry{
		offer("old", ChannelTip, "quota_75", t0), seen("old", t0),
		offer("new", ChannelTip, "quota_75", now.Add(-time.Hour)), seen("new", now),
	}
	got := Prune(es, now, 90*24*time.Hour)
	if len(got) != 2 || got[1].Type != EntrySeen || got[1].ID != "new" {
		t.Fatalf("Prune = %+v", got)
	}
}

func TestFoldKeepsTheFirstResolve(t *testing.T) {
	got := Fold([]Entry{
		offer("a", ChannelAdvice, "lookup", t0),
		resolve("a", OutcomeFollowed, t0.Add(time.Minute)),
		resolve("a", OutcomeIgnored, t0.Add(time.Hour)),
	}, t0.Add(2*time.Hour))
	if got[0].Outcome != OutcomeFollowed {
		t.Fatalf("outcome = %s; want the first recorded one", got[0].Outcome)
	}
}

func ignoredRun(n int, kind string, start time.Time) []Entry {
	es := make([]Entry, 0, 2*n)
	for i := range n {
		id := fmt.Sprintf("%s-%d", kind, i)
		at := start.Add(time.Duration(i) * time.Hour)
		es = append(es, offer(id, ChannelAdvice, kind, at), resolve(id, OutcomeIgnored, at.Add(time.Minute)))
	}
	return es
}

func TestQuieted(t *testing.T) {
	now := t0.Add(10 * time.Hour)
	if Quieted(Fold(ignoredRun(QuietAfter-1, "lookup", t0), now), "models", "lookup", now) {
		t.Error("quieted before QuietAfter ignored offers")
	}
	run := ignoredRun(QuietAfter, "lookup", t0)
	if !Quieted(Fold(run, now), "models", "lookup", now) {
		t.Error("not quieted after QuietAfter ignored offers")
	}
	if Quieted(Fold(run, now), "models", "research", now) {
		t.Error("quieting leaked to another kind")
	}
	followed := append(run, offer("f", ChannelAdvice, "lookup", t0.Add(8*time.Hour)), resolve("f", OutcomeFollowed, t0.Add(9*time.Hour)))
	if Quieted(Fold(followed, now), "models", "lookup", now) {
		t.Error("still quiet after the latest offer was followed")
	}
	later := t0.Add(QuietLookback + 10*time.Hour)
	if Quieted(Fold(run, later), "models", "lookup", later) {
		t.Error("still quiet after the evidence aged out")
	}
}

func TestQuietedIgnoresUnknownOutcomes(t *testing.T) {
	run := ignoredRun(QuietAfter-1, "lookup", t0)
	run = append(run, offer("u", ChannelAdvice, "lookup", t0.Add(-time.Hour)))
	now := t0.Add(OpenFor + time.Hour)
	if Quieted(Fold(run, now), "models", "lookup", now) {
		t.Error("an unknown outcome counted as ignored")
	}
}

func TestSummarize(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	es := append(ignoredRun(QuietAfter, "lookup", t0),
		offer("m", ChannelMove, "lookup", t0),
		offer("r", ChannelAdvice, "research", t0), resolve("r", OutcomeFollowed, t0.Add(time.Minute)))
	got := Summarize(Fold(es, now), now)
	if len(got) != 3 {
		t.Fatalf("summaries = %+v; want 3", got)
	}
	lookup, research, move := got[0], got[1], got[2]
	if lookup.Kind != "lookup" || lookup.Channel != ChannelAdvice || lookup.Ignored != QuietAfter || !lookup.Quiet {
		t.Errorf("lookup advice = %+v", lookup)
	}
	if research.Followed != 1 || research.Quiet {
		t.Errorf("research advice = %+v", research)
	}
	if move.Channel != ChannelMove || move.Stood != 1 || move.Quiet || move.Resolved() != 1 {
		t.Errorf("move = %+v", move)
	}
}

func TestPruneKeepsResolvesWithTheirOffers(t *testing.T) {
	now := t0.Add(100 * 24 * time.Hour)
	es := []Entry{
		offer("old", ChannelAdvice, "lookup", t0), resolve("old", OutcomeIgnored, t0.Add(time.Minute)),
		offer("new", ChannelAdvice, "lookup", now.Add(-time.Hour)), resolve("new", OutcomeFollowed, now),
		{Type: EntryLowered, At: t0, Power: "models"},
		{Type: EntryLowered, At: now, Power: "models"},
	}
	got := Prune(es, now, 90*24*time.Hour)
	if len(got) != 3 || got[0].ID != "new" || got[1].ID != "new" || got[2].Type != EntryLowered {
		t.Fatalf("Prune = %+v", got)
	}
}
