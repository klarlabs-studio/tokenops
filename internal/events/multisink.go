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

// AppendBatch fans envs out to every wrapped sink. Each sink receives
// an independent deep copy of the batch so a mutating sink (e.g. the
// OTLP exporter's redactor) cannot pollute the view a sibling sink
// sees. Errors are joined.
func (m *MultiSink) AppendBatch(ctx context.Context, envs []*eventschema.Envelope) error {
	if len(m.sinks) == 0 {
		return nil
	}
	var errs []string
	for _, s := range m.sinks {
		clones := cloneBatch(envs)
		if err := s.AppendBatch(ctx, clones); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New("events: multisink: " + strings.Join(errs, "; "))
	}
	return nil
}

func cloneBatch(envs []*eventschema.Envelope) []*eventschema.Envelope {
	out := make([]*eventschema.Envelope, 0, len(envs))
	for _, env := range envs {
		c, err := env.Clone()
		if err != nil {
			// Clone failure means malformed payload — fall back to
			// sharing the pointer; downstream sink will fail to
			// serialise it the same way.
			out = append(out, env)
			continue
		}
		out = append(out, c)
	}
	return out
}
