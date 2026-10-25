package moveverify

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/moveverdict"
)

func TestSessionFilesRoundTripAndAgeOut(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	now := time.Now()
	f := s.LoadSession("s1")
	f.Merge([]moveverdict.Observation{{Ref: "a", Trials: 1}, {Ref: "b", Trials: 1}})
	f.Merge([]moveverdict.Observation{{Ref: "a", Trials: 1, Bad: 1}})
	if len(f.Calls) != 2 || f.Calls[0].Bad != 0 {
		t.Fatalf("merge replaced or duplicated a settled observation: %+v", f.Calls)
	}
	if err := s.SaveSession(f); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSession(SessionFile{Session: "old"}); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(s.Dir, "sessions", "old.json")
	stale := now.Add(-moveverdict.Lookback - moveverdict.MaxRun - time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}
	if got := s.Sessions(now, false); len(got) != 1 {
		t.Fatalf("read-only sessions = %+v", got)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("a read-only read removed a file: %v", err)
	}
	got := s.Sessions(now, true)
	if len(got) != 1 || got[0].Session != "s1" || len(got[0].Calls) != 2 {
		t.Fatalf("sessions = %+v", got)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("stale session file kept: %v", err)
	}
	if err := s.SaveSession(SessionFile{Session: "../escape"}); err == nil {
		t.Error("saved a session id that is not a plain name")
	}
}

func TestRetractedReadsActiveRetractionsOnly(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	k := moveverdict.Key{Kind: "edit", From: "opus", To: "sonnet"}
	ended := moveverdict.Key{Kind: "deep", From: "opus", To: "sonnet"}
	if err := s.Update(func(st *State) error {
		st.Retractions = append(st.Retractions, moveverdict.Retraction{ID: "d1", Key: k, At: time.Now()},
			moveverdict.Retraction{ID: "d2", Key: ended, At: time.Now(), Ended: time.Now(), EndedBy: moveverdict.EndedByOperator})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r := s.Retracted()
	if !r("edit", "opus", "sonnet") || r("deep", "opus", "sonnet") || r("edit", "opus", "haiku") {
		t.Fatal("retracted read wrong")
	}
	if (Store{}).Retracted()("edit", "opus", "sonnet") {
		t.Fatal("no store retracted a move")
	}
}
