package headroom

import (
	"context"
	"runtime"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type tallyReader struct {
	events      []*eventschema.Envelope
	reads, cnts int
}

func (r *tallyReader) ReadEvents(_ context.Context, _ eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	r.reads++
	var out []*eventschema.Envelope
	for _, e := range r.events {
		if !e.Timestamp.Before(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (r *tallyReader) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	r.cnts++
	return map[string]int64{"x": 1}, nil
}

// Every question inside the memo's reach is answered from one read, with
// the same events a direct read returns; an older one goes to the store.
func TestMemoReaderReadsOnce(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *eventschema.Envelope { return &eventschema.Envelope{Timestamp: now.Add(-d)} }
	r := &tallyReader{events: []*eventschema.Envelope{at(30 * 24 * time.Hour), at(10 * 24 * time.Hour), at(5 * time.Hour), at(time.Minute)}}
	m := newMemoReader(r, now)
	ctx := context.Background()

	for since, want := range map[time.Duration]int{19 * 24 * time.Hour: 3, 7 * 24 * time.Hour: 2, 30 * time.Minute: 1} {
		got, _ := m.ReadEvents(ctx, eventschema.EventTypePrompt, now.Add(-since))
		if len(got) != want {
			t.Errorf("since %v: %d events, want %d", since, len(got), want)
		}
	}
	if r.reads != 1 {
		t.Errorf("store read %d times, want once", r.reads)
	}
	if got, _ := m.ReadEvents(ctx, eventschema.EventTypePrompt, now.Add(-40*24*time.Hour)); len(got) != 4 || r.reads != 2 {
		t.Errorf("an older question: %d events, %d reads", len(got), r.reads)
	}
	a, b := now.Add(-time.Hour), now
	_, _ = m.CountBySource(ctx, a, b)
	_, _ = m.CountBySource(ctx, a, b)
	if r.cnts != 1 {
		t.Errorf("counted %d times, want once", r.cnts)
	}
	if newMemoReader(m, now) != m {
		t.Error("a memo was wrapped in another")
	}
}

// A read the memo answers returns the same events, in the same order, as
// the store would; and repeated reads do not copy the month again each time.
func TestMemoReaderAnswersLikeTheStoreAndDoesNotCopyPerRead(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	var events []*eventschema.Envelope
	for i := 0; i < 50_000; i++ {
		events = append(events, &eventschema.Envelope{Timestamp: now.Add(-time.Duration(50_000-i) * time.Minute)})
	}
	r := &tallyReader{events: events}
	m := newMemoReader(r, now).(*memoReader)
	ctx := context.Background()
	if _, err := m.ReadEvents(ctx, eventschema.EventTypePrompt, m.floor); err != nil {
		t.Fatal(err)
	}
	for _, since := range []time.Time{m.floor, now.Add(-24 * time.Hour), now.Add(-time.Hour), now} {
		got, _ := m.ReadEvents(ctx, eventschema.EventTypePrompt, since)
		want, _ := r.ReadEvents(ctx, eventschema.EventTypePrompt, since)
		if len(got) != len(want) {
			t.Fatalf("since %s: %d events, store has %d", since, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("since %s: event %d differs from the store's", since, i)
			}
		}
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < 100; i++ {
		_, _ = m.ReadEvents(ctx, eventschema.EventTypePrompt, now.Add(-time.Hour))
	}
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > 64<<10 {
		t.Errorf("100 reads of a 50k-event window allocated %d bytes; a read should not copy the window", got)
	}
}

// A reader whose events are not in time order still gets the filter's
// answer, in the order the store gave them.
func TestMemoReaderKeepsStoreOrderWhenUnsorted(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *eventschema.Envelope { return &eventschema.Envelope{Timestamp: now.Add(-d)} }
	r := &tallyReader{events: []*eventschema.Envelope{at(time.Hour), at(3 * time.Hour), at(2 * time.Hour)}}
	m := newMemoReader(r, now).(*memoReader)
	got, _ := m.ReadEvents(context.Background(), eventschema.EventTypePrompt, now.Add(-3*time.Hour))
	if len(got) != 3 || got[0] != r.events[0] || got[1] != r.events[1] || got[2] != r.events[2] {
		t.Errorf("unsorted events came back reordered or filtered wrongly: %d events", len(got))
	}
}
