package boundaryledger

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
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

func TestFiguresRoundTrip(t *testing.T) {
	d := Dir(t.TempDir())
	if _, ok, err := d.GetFigures(); ok || err != nil {
		t.Fatalf("missing figures = ok %v, err %v", ok, err)
	}
	at := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	in := Figures{
		ObservedAt:    at,
		PlanLeftPct:   map[string]map[string]float64{"anthropic": {"5h": 40, "week": 7}},
		BudgetUsedPct: map[string]float64{"monthly": 91.5},
	}
	if err := d.PutFigures(in); err != nil {
		t.Fatal(err)
	}
	got, ok, err := d.GetFigures()
	if err != nil || !ok {
		t.Fatalf("GetFigures = ok %v, err %v", ok, err)
	}
	if !got.ObservedAt.Equal(at) || got.PlanLeftPct["anthropic"]["week"] != 7 || got.BudgetUsedPct["monthly"] != 91.5 {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

// TestAddMatchesCountsEveryParallelCall pins what the match log is for:
// parallel tool calls in one session each count, where rewriting one
// counter file let the last writer drop the others' counts.
func TestAddMatchesCountsEveryParallelCall(t *testing.T) {
	d := Dir(t.TempDir())
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := d.AddMatches("s1", []string{"spend", "floor"}, at.Add(time.Duration(i)*time.Second)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	s := mustSessions(t, d)["s1"]
	if s.Matches["spend"] != 50 || s.Matches["floor"] != 50 {
		t.Fatalf("matches = %v, want 50 each", s.Matches)
	}
	if want := at.Add(49 * time.Second); !s.LastAt.Equal(want) {
		t.Fatalf("last at = %s, want %s", s.LastAt, want)
	}
}

// TestSessionsAddLegacyCountsToTheLog keeps the counts a session file
// holds from before the log, and its decision stages.
func TestSessionsAddLegacyCountsToTheLog(t *testing.T) {
	d := Dir(t.TempDir())
	old := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	if err := d.PutSession("s1", Session{Matches: map[string]int{"spend": 3}, Recorded: map[string]string{"bnd-x": "shadow"}, LastAt: old}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddMatches("s1", []string{"spend"}, old.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := d.AddMatches("s2", []string{"floor"}, old); err != nil {
		t.Fatal(err)
	}
	got := mustSessions(t, d)
	if s := got["s1"]; s.Matches["spend"] != 4 || s.Recorded["bnd-x"] != "shadow" || !s.LastAt.Equal(old.Add(time.Hour)) {
		t.Fatalf("s1 = %+v", s)
	}
	if s := got["s2"]; s.Matches["floor"] != 1 {
		t.Fatalf("s2 = %+v", s)
	}
}

// TestSessionsSkipATornMatchLine counts the whole lines around one a crash
// cut short.
func TestSessionsSkipATornMatchLine(t *testing.T) {
	d := Dir(t.TempDir())
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	if err := d.AddMatches("s1", []string{"spend"}, at); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(string(d), "sessions", "s1.matches")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"at":"2026-10-17T12:00:01Z","rules":["spe`)
	_ = f.Close()
	if err := d.AddMatches("s1", []string{"spend"}, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if n := mustSessions(t, d)["s1"].Matches["spend"]; n < 1 {
		t.Fatalf("spend = %d, want the whole lines counted", n)
	}
}

func mustSessions(t *testing.T, d Dir) map[string]Session {
	t.Helper()
	got, err := d.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestAMatchLogIsFoldedPastItsCap keeps a long session's log small: past
// its cap the lines fold into one line of totals, and nothing is lost.
func TestAMatchLogIsFoldedPastItsCap(t *testing.T) {
	d := Dir(t.TempDir())
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	const n = 3000
	for i := range n {
		if err := d.AddMatches("s1", []string{"spend"}, at.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	fi, err := os.Stat(filepath.Join(string(d), "sessions", "s1.matches"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > matchLogCap {
		t.Fatalf("log is %d bytes, cap %d", fi.Size(), matchLogCap)
	}
	s := mustSessions(t, d)["s1"]
	if s.Matches["spend"] != n || !s.LastAt.Equal(at.Add((n-1)*time.Second)) {
		t.Fatalf("after folding: %+v", s)
	}
}

// TestRecentCallsKeepTheNewestWindow pins the loop figure's input: the
// session's latest calls, oldest first, at most a window of them, each a
// tool name and a hash.
func TestRecentCallsKeepTheNewestWindow(t *testing.T) {
	d := Dir(t.TempDir())
	if got, err := d.RecentCalls("s1"); err != nil || len(got) != 0 {
		t.Fatalf("no calls yet = %v, %v", got, err)
	}
	total := boundaries.LoopWindow*3 + 5
	for i := range total {
		if err := d.AppendCall("s1", boundaries.ToolCall{Tool: "Bash", Hash: fmt.Sprintf("%016x", i)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.RecentCalls("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != boundaries.LoopWindow {
		t.Fatalf("%d calls read, want %d", len(got), boundaries.LoopWindow)
	}
	if want := fmt.Sprintf("%016x", total-1); got[len(got)-1].Hash != want {
		t.Fatalf("newest = %+v, want hash %s", got[len(got)-1], want)
	}
	if other, _ := d.RecentCalls("s2"); len(other) != 0 {
		t.Fatalf("another session read %d calls", len(other))
	}
}

// TestStaleCallLogsArePruned: a session idle for a day has no live loop,
// so its call log goes; a fresh one stays; and the scan runs at most once
// an hour, by the stamp beside the logs.
func TestStaleCallLogsArePruned(t *testing.T) {
	d := Dir(t.TempDir())
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	for _, s := range []string{"stale", "fresh"} {
		if err := d.AppendCall(s, boundaries.ToolCall{Tool: "Bash", Hash: "0123456789abcdef"}); err != nil {
			t.Fatal(err)
		}
	}
	age := func(session string, by time.Duration) {
		t.Helper()
		if err := os.Chtimes(d.callLogPath(session), now.Add(-by), now.Add(-by)); err != nil {
			t.Fatal(err)
		}
	}
	age("stale", 25*time.Hour)
	age("fresh", time.Hour)
	if err := d.PruneCalls(now); err != nil {
		t.Fatal(err)
	}
	if calls, _ := d.RecentCalls("stale"); len(calls) != 0 {
		t.Fatal("a log idle for 25h was kept")
	}
	if calls, _ := d.RecentCalls("fresh"); len(calls) != 1 {
		t.Fatal("a log used an hour ago was removed")
	}

	// Within the hour nothing is scanned, so a log gone stale since stays.
	age("fresh", 30*time.Hour)
	if err := d.PruneCalls(now.Add(30 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if calls, _ := d.RecentCalls("fresh"); len(calls) != 1 {
		t.Fatal("pruned again within the hour")
	}
	if err := d.PruneCalls(now.Add(61 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if calls, _ := d.RecentCalls("fresh"); len(calls) != 0 {
		t.Fatal("not pruned after the hour")
	}
	// No logs at all is no error.
	if err := Dir(t.TempDir()).PruneCalls(now); err != nil {
		t.Fatal(err)
	}
}
