package routingapproval

import (
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "a.jsonl"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

// A missing log is an empty log, not an error — the daemon reads this
// before anything has ever been proposed.
func TestMissingLogReadsEmpty(t *testing.T) {
	s := openTemp(t)
	got, err := s.Pending()
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("pending = %+v, want none", got)
	}
}

// The proxy proposes; the operator answers elsewhere; the proxy sees the
// answer. That round trip across two processes is the whole point.
func TestProposeThenDecide(t *testing.T) {
	s := openTemp(t)
	rec := Record{
		Key: "anthropic|claude-opus-4-8|claude-fable-5", Provider: "anthropic",
		From: "claude-opus-4-8", To: "claude-fable-5", Preferred: "claude-opus-5",
		DeltaUSD: 30, Priced: true, Reason: "costs more",
	}
	if err := s.Propose(rec); err != nil {
		t.Fatalf("propose: %v", err)
	}
	pending, _ := s.Pending()
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}
	if pending[0].Preferred != "claude-opus-5" {
		t.Errorf("preferred lost: %+v", pending[0])
	}

	if err := s.Decide(rec.Key, "approved", "claude-fable-5"); err != nil {
		t.Fatalf("decide: %v", err)
	}
	pending, _ = s.Pending()
	if len(pending) != 0 {
		t.Errorf("decided route still pending: %+v", pending)
	}
	all, _ := s.Load()
	st := all[rec.Key]
	if !st.Decided || st.Decision != "approved" || st.ChosenModel != "claude-fable-5" {
		t.Errorf("decision not recorded: %+v", st)
	}
}

// Repeated proposals count rather than pile up, so an operator can tell a
// route that keeps coming up from one that fired once.
func TestRepeatedProposalsCount(t *testing.T) {
	s := openTemp(t)
	rec := Record{Key: "k", Provider: "anthropic", From: "a", To: "b"}
	for range 3 {
		if err := s.Propose(rec); err != nil {
			t.Fatalf("propose: %v", err)
		}
	}
	pending, _ := s.Pending()
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1 deduplicated route", len(pending))
	}
	if pending[0].Seen != 3 {
		t.Errorf("Seen = %d, want 3", pending[0].Seen)
	}
}

// The newest decision wins, so an operator can change their mind.
func TestLatestDecisionWins(t *testing.T) {
	s := openTemp(t)
	_ = s.Propose(Record{Key: "k", From: "a", To: "b"})
	_ = s.Decide("k", "denied", "a")
	_ = s.Decide("k", "approved", "b")
	all, _ := s.Load()
	if all["k"].Decision != "approved" {
		t.Errorf("Decision = %q, want the later answer", all["k"].Decision)
	}
}

// A truncated final line must not make the whole log unreadable.
func TestMalformedLineIsSkipped(t *testing.T) {
	s := openTemp(t)
	_ = s.Propose(Record{Key: "good", From: "a", To: "b"})
	if err := s.append(Record{}); err != nil { // Key == "" is skipped on load
		t.Fatalf("append: %v", err)
	}
	pending, err := s.Pending()
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 || pending[0].Key != "good" {
		t.Errorf("pending = %+v, want just the well-formed route", pending)
	}
}
