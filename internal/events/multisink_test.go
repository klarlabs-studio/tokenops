package events

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

type recordingSink struct {
	calls atomic.Int64
	err   error
}

func (r *recordingSink) AppendBatch(_ context.Context, envs []*eventschema.Envelope) error {
	r.calls.Add(int64(len(envs)))
	return r.err
}

func TestMultiSinkFansOut(t *testing.T) {
	a := &recordingSink{}
	b := &recordingSink{}
	ms := NewMultiSink(a, b, nil)

	envs := []*eventschema.Envelope{{}, {}}
	if err := ms.AppendBatch(context.Background(), envs); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}
	if a.calls.Load() != 2 || b.calls.Load() != 2 {
		t.Errorf("calls = a:%d b:%d", a.calls.Load(), b.calls.Load())
	}
}

func TestMultiSinkAggregatesErrors(t *testing.T) {
	ms := NewMultiSink(
		&recordingSink{err: errors.New("sink-a fail")},
		&recordingSink{err: errors.New("sink-b fail")},
	)
	err := ms.AppendBatch(context.Background(), []*eventschema.Envelope{{}})
	if err == nil {
		t.Fatal("expected aggregated error")
	}
	for _, want := range []string{"sink-a fail", "sink-b fail"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// mutatingSink mimics a Sink that rewrites payload fields (e.g. the
// OTLP exporter's redactor). After a mutate-and-share era, a sibling
// sink would see the mutation. With clone fan-out it must not.
type mutatingSink struct{}

func (mutatingSink) AppendBatch(_ context.Context, envs []*eventschema.Envelope) error {
	for _, env := range envs {
		if pe, ok := env.Payload.(*eventschema.PromptEvent); ok {
			pe.PromptHash = "MUTATED"
		}
	}
	return nil
}

type readingSink struct{ saw string }

func (r *readingSink) AppendBatch(_ context.Context, envs []*eventschema.Envelope) error {
	for _, env := range envs {
		if pe, ok := env.Payload.(*eventschema.PromptEvent); ok {
			r.saw = pe.PromptHash
		}
	}
	return nil
}

func TestMultiSinkIsolatesMutatingSinks(t *testing.T) {
	mutator := mutatingSink{}
	reader := &readingSink{}
	multi := NewMultiSink(mutator, reader)
	env := &eventschema.Envelope{
		ID:            "1",
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypePrompt,
		Payload:       &eventschema.PromptEvent{PromptHash: "sha256:abc"},
	}
	if err := multi.AppendBatch(context.Background(), []*eventschema.Envelope{env}); err != nil {
		t.Fatal(err)
	}
	if reader.saw == "MUTATED" {
		t.Errorf("reader sink saw mutation from sibling sink")
	}
	if reader.saw != "sha256:abc" {
		t.Errorf("reader sink saw %q, want sha256:abc", reader.saw)
	}
	// Original envelope untouched.
	if env.Payload.(*eventschema.PromptEvent).PromptHash != "sha256:abc" {
		t.Errorf("original envelope mutated")
	}
}

func TestMultiSinkEmpty(t *testing.T) {
	ms := NewMultiSink()
	if err := ms.AppendBatch(context.Background(), []*eventschema.Envelope{{}}); err != nil {
		t.Errorf("empty multisink should swallow batch, got %v", err)
	}
}
