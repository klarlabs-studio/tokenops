package sqlite

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// RecordedEvents answers EachMatching for a source's events from an
// EventCache, and every other read from the store.
//
// The waste report reads every plan window reading of the last month,
// source by source: one of the sources is the Codex reader, whose every
// turn carries the window, so it read and decoded hundreds of thousands
// of events per report. The glance's EventCache already holds those
// prompt events; RecordedEvents filters them by source, and reads from
// the store only the source's events of other types, which are few and
// found by type.
//
// The envelopes it hands out for a source are the cache's: shared, not
// to be modified, and without the attributes the cache leaves out
// (uncachedAttributes). The plan window readings it is read for are kept.
type RecordedEvents struct {
	cache *EventCache
}

// NewRecordedEvents reads through cache.
func NewRecordedEvents(cache *EventCache) *RecordedEvents { return &RecordedEvents{cache: cache} }

// EachMatching calls fn with every event f matches, oldest first, as
// Store.EachMatching does. A filter by source and time alone is answered
// from the cache; any other goes to the store.
func (r *RecordedEvents) EachMatching(ctx context.Context, f Filter, fn func(*eventschema.Envelope) error) error {
	if !bySourceOnly(f) {
		return r.cache.store.EachMatching(ctx, f, fn)
	}
	until := int64(math.MaxInt64)
	if !f.Until.IsZero() {
		until = f.Until.UTC().UnixNano()
	}
	var prompts []*eventschema.Envelope
	answered, err := r.cache.view(ctx, eventschema.EventTypePrompt, f.Since.UTC().UnixNano(), func(rows []cachedEvent) {
		for _, e := range rows {
			if e.unixNano() >= until {
				break
			}
			if e.env.Source == f.Source {
				prompts = append(prompts, e.env)
			}
		}
	})
	if err != nil {
		return err
	}
	if !answered {
		return r.cache.store.EachMatching(ctx, f, fn)
	}
	others, err := r.cache.store.otherThan(ctx, eventschema.EventTypePrompt, f)
	if err != nil {
		return err
	}
	// Both are in (timestamp, id) order: merge them into it.
	i, j := 0, 0
	for i < len(prompts) || j < len(others) {
		var next *eventschema.Envelope
		if j == len(others) || (i < len(prompts) && compareCached(cachedEvent{env: prompts[i]}, cachedEvent{env: others[j]}) < 0) {
			next, i = prompts[i], i+1
		} else {
			next, j = others[j], j+1
		}
		if err := fn(next); err != nil {
			return err
		}
	}
	return nil
}

// bySourceOnly reports whether f selects by source and time alone, from
// a known start.
func bySourceOnly(f Filter) bool {
	if f.Source == "" || f.Since.IsZero() {
		return false
	}
	rest := f
	rest.Source, rest.Since, rest.Until, rest.Limit = "", time.Time{}, time.Time{}, 0
	return reflect.ValueOf(rest).IsZero()
}

// otherTypeSelects are selectSQL on the (type, timestamp) index: the
// events below a type and above it. Events of other types than prompt
// are a few hundred in a store of hundreds of thousands, so each range
// is short.
var otherTypeSelects = [2]struct{ sel, cond string }{
	{strings.Replace(selectSQL, "FROM events", "FROM events INDEXED BY events_type_ts_idx", 1), "type < ?"},
	{strings.Replace(selectSQL, "FROM events", "FROM events INDEXED BY events_type_ts_idx", 1), "type > ?"},
}

// otherThan is what EachMatching(f) calls fn with of the events whose
// type is not t, oldest first. f has no Type.
func (s *Store) otherThan(ctx context.Context, t eventschema.EventType, f Filter) ([]*eventschema.Envelope, error) {
	conds, args := f.where()
	after := f.Since.UTC().UnixNano() - 1
	var out []*eventschema.Envelope
	for _, q := range otherTypeSelects {
		err := readPages(ctx, s.db, q.sel, append([]string{q.cond}, conds...), append([]any{string(t)}, args...), after,
			func(rs *sql.Rows) (*eventschema.Envelope, error) {
				env, err := scanEnvelope(rs)
				if err == nil {
					out = append(out, env)
				}
				return env, err
			})
		if err != nil {
			return nil, err
		}
	}
	slices.SortFunc(out, func(a, b *eventschema.Envelope) int {
		return compareCached(cachedEvent{env: a}, cachedEvent{env: b})
	})
	return out, nil
}
