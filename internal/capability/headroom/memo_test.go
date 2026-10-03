package headroom

import (
	"context"
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
