package boundaryledger

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func decision(id, envID string) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: envID, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypeDecision,
		Timestamp: time.Unix(100, 0).UTC(), Source: "boundary",
		Correlation: eventschema.Correlation{Decision: id},
		Payload:     &eventschema.DecisionEvent{Kind: "boundary", Stage: eventschema.DecisionStageShadow},
	}
}

func TestLedgerAppendsAndReadsByDecision(t *testing.T) {
	d := Dir(t.TempDir())
	for i, id := range []string{"bnd-a", "bnd-b", "bnd-a"} {
		if err := d.Append(decision(id, fmt.Sprint(i))); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.Decision("bnd-a")
	if err != nil || len(got) != 2 {
		t.Fatalf("Decision = %d, %v", len(got), err)
	}
	if _, ok := got[0].Payload.(*eventschema.DecisionEvent); !ok {
		t.Fatalf("payload = %T", got[0].Payload)
	}
	st, err := os.Stat(filepath.Join(string(d), ledgerFile))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("ledger mode = %v, %v", st.Mode().Perm(), err)
	}
}

func TestConcurrentAppendsAllLand(t *testing.T) {
	d := Dir(t.TempDir())
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Go(func() { _ = d.Append(decision("bnd-x", fmt.Sprint(i))) })
	}
	wg.Wait()
	got, _ := d.Read()
	if len(got) != 30 {
		t.Fatalf("read %d; want 30", len(got))
	}
}

func TestATornLineIsSkipped(t *testing.T) {
	d := Dir(t.TempDir())
	_ = d.Append(decision("bnd-a", "1"))
	f, _ := os.OpenFile(filepath.Join(string(d), ledgerFile), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(`{"id":"torn`)
	_ = f.Close()
	got, err := d.Read()
	if err != nil || len(got) != 1 {
		t.Fatalf("Read = %d, %v", len(got), err)
	}
}

func TestAnswersExpireAndOneOffsAreSpentOnce(t *testing.T) {
	d := Dir(t.TempDir())
	now := time.Now()
	if err := d.PutAnswer(Answer{ID: "bnd-a", Approved: true, Scope: ScopeOnce, At: now}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := d.GetAnswer("bnd-a", now.Add(25*time.Hour)); ok {
		t.Fatal("an answer outlived its TTL")
	}
	if a, ok, _ := d.GetAnswer("bnd-a", now); !ok || !a.Approved {
		t.Fatalf("answer = %+v, %v", a, ok)
	}
	var wins int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if ok, _ := d.ConsumeAnswer("bnd-a"); ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("one-off answer spent %d times", wins)
	}
	if _, ok, _ := d.GetAnswer("bnd-a", now); ok {
		t.Fatal("spent answer still readable")
	}
}

func TestIDsCannotEscapeTheDirectory(t *testing.T) {
	d := Dir(t.TempDir())
	for _, id := range []string{"../x", "a/b", ""} {
		if err := d.PutPending(Pending{ID: id}); err == nil {
			t.Errorf("PutPending(%q) accepted", id)
		}
	}
}

func TestPendingListsNewestFirst(t *testing.T) {
	d := Dir(t.TempDir())
	_ = d.PutPending(Pending{ID: "bnd-old", At: time.Unix(1, 0)})
	_ = d.PutPending(Pending{ID: "bnd-new", At: time.Unix(2, 0)})
	got, err := d.ListPending()
	if err != nil || len(got) != 2 || got[0].ID != "bnd-new" {
		t.Fatalf("ListPending = %+v, %v", got, err)
	}
}

func TestSessionsRoundTrip(t *testing.T) {
	d := Dir(t.TempDir())
	if err := d.PutSession("s/1", Session{Matches: map[string]int{"r": 2}}); err != nil {
		t.Fatal(err)
	}
	s, err := d.GetSession("s/1")
	if err != nil || s.Matches["r"] != 2 {
		t.Fatalf("GetSession = %+v, %v", s, err)
	}
	all, _ := d.Sessions()
	if len(all) != 1 {
		t.Fatalf("Sessions = %+v", all)
	}
}

type fakeBus struct {
	events.Bus
	got []*eventschema.Envelope
}

func (b *fakeBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.got = append(b.got, env)
	return nil
}

func TestPollerSendsEachDecisionOnce(t *testing.T) {
	d := Dir(t.TempDir())
	bus := &fakeBus{}
	p := NewPoller(bus, d, 0, nil)
	_ = d.Append(decision("bnd-a", "1"))
	p.Once(context.Background())
	_ = d.Append(decision("bnd-a", "2"))
	p.Once(context.Background())
	if len(bus.got) != 2 || bus.got[1].ID != "2" {
		t.Fatalf("published %d", len(bus.got))
	}
}

func TestLedgerIsPrunedPastItsCap(t *testing.T) {
	d := Dir(t.TempDir())
	path := filepath.Join(string(d), ledgerFile)
	if err := os.MkdirAll(string(d), 0o700); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Create(path)
	pad := make([]byte, 1000)
	for i := range pad {
		pad[i] = 'x'
	}
	for i := 0; i < keepLines+1000; i++ {
		fmt.Fprintf(f, `{"id":"old-%d","type":"decision","payload":{"rationale":%q}}`+"\n", i, pad)
	}
	_ = f.Close()
	if err := d.Append(decision("bnd-new", "new")); err != nil {
		t.Fatal(err)
	}
	got, _ := d.Read()
	if len(got) != keepLines || got[len(got)-1].ID != "new" {
		t.Fatalf("after prune: %d lines, last %q", len(got), got[len(got)-1].ID)
	}
}
