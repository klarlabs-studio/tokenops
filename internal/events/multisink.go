package events

import (
	"context"
	"errors"
	"strings"

	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// MultiSink fans a single AppendBatch call out to N underlying sinks.
// It is the construction point for daemons that want envelopes shipped
// both to local SQLite and to a remote OTLP collector.
//
// Each sink receives the same batch; errors are aggregated and returned
// joined so an OTLP outage does not lose the SQLite write (the bus
// treats Sink errors as transient — the worker logs and moves on).
type MultiSink struct {
	sinks []Sink
}

// NewMultiSink builds a fan-out sink. nil sinks are silently dropped so
// callers can pass conditionally-built sinks without nil-checking.
func NewMultiSink(sinks ...Sink) *MultiSink {
	out := make([]Sink, 0, len(sinks))
	for _, s := range sinks {
		if s != nil {
			out = append(out, s)
		}
	}
	return &MultiSink{sinks: out}
}

// AppendBatch fans envs out to every wrapped sink. All sinks see every
// batch; errors from any are joined and returned.
func (m *MultiSink) AppendBatch(ctx context.Context, envs []*eventschema.Envelope) error {
	if len(m.sinks) == 0 {
		return nil
	}
	var errs []string
	for _, s := range m.sinks {
		if err := s.AppendBatch(ctx, envs); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New("events: multisink: " + strings.Join(errs, "; "))
	}
	return nil
}
