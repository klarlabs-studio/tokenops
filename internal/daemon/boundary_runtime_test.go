package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type countingSink struct{ batches int }

func (c *countingSink) AppendBatch(context.Context, []*eventschema.Envelope) error {
	c.batches++
	return nil
}

func TestSharedSinkHoldsWhileTheBoundaryRefuses(t *testing.T) {
	inner := &countingSink{}
	refuse := true
	asked := 0
	s := &sharedSink{inner: inner, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), allow: func() error {
		asked++
		if refuse {
			return errors.New("held")
		}
		return nil
	}}
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	if !s.isHeld(t0) || !s.isHeld(t0.Add(30*time.Second)) {
		t.Fatal("a refused export was let through")
	}
	if asked != 1 {
		t.Fatalf("asked %d times within a minute; want 1", asked)
	}
	refuse = false
	if s.isHeld(t0.Add(shareGateEvery)) {
		t.Fatal("an allowed export stayed held after a minute")
	}
	_ = s.AppendBatch(context.Background(), nil)
	if inner.batches != 1 {
		t.Fatalf("batches = %d", inner.batches)
	}
}
