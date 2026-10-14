package coach

import (
	"testing"
	"time"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

func TestAnswerTip(t *testing.T) {
	l := &memLedger{}
	RecordTip(l, now.Add(-2*time.Hour), "old", "s1", "budget_50", ViaStopHook)
	RecordTip(l, now.Add(-time.Hour), "new", "s1", "budget_75", ViaStopHook)

	// No id answers the newest unanswered tip.
	r, err := AnswerTip(l, now, "", AnswerExpected, 0)
	if err != nil || r.ID != "new" || r.Answer != AnswerExpected || r.Kind != "budget_75" {
		t.Fatalf("AnswerTip = %+v, %v", r, err)
	}
	// Then the next newest.
	r, err = AnswerTip(l, now, "", AnswerSnoozed, 0)
	if err != nil || r.ID != "old" || r.SnoozedUntil == nil || !r.SnoozedUntil.Equal(now.Add(DefaultSnooze)) {
		t.Fatalf("second AnswerTip = %+v, %v", r, err)
	}
	if _, err := AnswerTip(l, now, "", AnswerAcknowledged, 0); err != ErrNoTip {
		t.Fatalf("answering with nothing left to answer = %v, want ErrNoTip", err)
	}
	if _, err := AnswerTip(l, now, "nope", AnswerAcknowledged, 0); err == nil {
		t.Fatal("an unknown id should be an error")
	}
	if _, err := AnswerTip(l, now, "new", "followed", 0); err == nil {
		t.Fatal("followed is inferred, never given")
	}
	// An explicit id may be re-answered, and the newest answer stands.
	if r, err := AnswerTip(l, now.Add(time.Minute), "new", AnswerAcknowledged, 0); err != nil || r.Answer != AnswerAcknowledged {
		t.Fatalf("re-answer = %+v, %v", r, err)
	}
	recs := ft.Fold(l.entries, now.Add(time.Hour))
	if recs[1].Answer != ft.OutcomeAcknowledged {
		t.Fatalf("newest answer did not stand: %+v", recs[1])
	}
}

func TestHolderAndBudgetAnswers(t *testing.T) {
	l := &memLedger{}
	RecordTip(l, now.Add(-time.Hour), "a", "s1", "budget_50", ViaStopHook)
	if _, err := AnswerTip(l, now, "a", AnswerExpected, 0); err != nil {
		t.Fatal(err)
	}
	held := Holder(l, now)
	if got := held("s1", "budget_over"); got != ft.HeldEpisode {
		t.Errorf("held = %q", got)
	}
	if got := held("s2", "budget_50"); got != "" {
		t.Errorf("another session held = %q", got)
	}
	if e, a := BudgetAnswers(l, now); e != 1 || a != 1 {
		t.Errorf("BudgetAnswers = %d/%d", e, a)
	}
	if got := Holder(nil, now)("s1", "budget_50"); got != "" {
		t.Errorf("a missing ledger must hold nothing: %q", got)
	}
}
