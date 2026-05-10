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

func TestMultiSinkEmpty(t *testing.T) {
	ms := NewMultiSink()
	if err := ms.AppendBatch(context.Background(), []*eventschema.Envelope{{}}); err != nil {
		t.Errorf("empty multisink should swallow batch, got %v", err)
	}
}
