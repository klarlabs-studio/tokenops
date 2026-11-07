package sqlite

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// More rows than one page, many sharing a timestamp across the page
// boundary: every row comes back once, oldest first, and nothing before
// since.
func TestReadEventsHasNoCap(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	total := readPage*2 + 7
	envs := make([]*eventschema.Envelope, 0, total+1)
	for i := 0; i < total; i++ {
		// Groups of 1,000 share a timestamp, so pages split inside a group.
		at := base.Add(time.Duration(i/1000) * time.Second)
		envs = append(envs, &eventschema.Envelope{
			ID: fmt.Sprintf("e%06d", i), SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: at, Source: "test",
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
		})
	}
	envs = append(envs, &eventschema.Envelope{
		ID: "before", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: base.Add(-time.Hour), Source: "test",
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
	})
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadEvents(ctx, eventschema.EventTypePrompt, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != total {
		t.Fatalf("got %d events, want %d", len(got), total)
	}
	seen := map[string]bool{}
	for i, e := range got {
		if seen[e.ID] {
			t.Fatalf("%s returned twice", e.ID)
		}
		seen[e.ID] = true
		if i > 0 && e.Timestamp.Before(got[i-1].Timestamp) {
			t.Fatalf("out of order at %d", i)
		}
	}
	if got[len(got)-1].ID != fmt.Sprintf("e%06d", total-1) {
		t.Errorf("newest is %s", got[len(got)-1].ID)
	}
}

// EachEvent hands over exactly what ReadEvents returns, in order, across
// page boundaries, and stops at the first error of its callback.
func TestEachEventMatchesReadEvents(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	total := readPage + 13
	envs := make([]*eventschema.Envelope, 0, total)
	for i := range total {
		envs = append(envs, &eventschema.Envelope{
			ID: fmt.Sprintf("e%06d", i), SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypePrompt, Timestamp: base.Add(time.Duration(i/500) * time.Second), Source: "test",
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
		})
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}
	want, err := s.ReadEvents(ctx, eventschema.EventTypePrompt, base)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := s.EachEvent(ctx, eventschema.EventTypePrompt, base, func(e *eventschema.Envelope) error {
		got = append(got, e.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("EachEvent handed %d events, ReadEvents returns %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i].ID {
			t.Fatalf("event %d: %s, want %s", i, got[i], want[i].ID)
		}
	}
	stop := errors.New("stop")
	n := 0
	err = s.EachEvent(ctx, eventschema.EventTypePrompt, base, func(*eventschema.Envelope) error {
		n++
		if n == 3 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || n != 3 {
		t.Errorf("err %v after %d events, want stop after 3", err, n)
	}
}
