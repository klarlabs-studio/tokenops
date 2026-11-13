package plans

import (
	"slices"
	"sort"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// newestTwoReference is newestTwo as it was written before the sort went:
// candidates stably sorted newest first, then the same walk.
func newestTwoReference(events []*eventschema.Envelope, sources []string, usable func(*eventschema.Envelope) bool) (newest, prior map[string]*eventschema.Envelope) {
	type candidate struct {
		e   *eventschema.Envelope
		key string
	}
	var cands []candidate
	for _, e := range events {
		if e == nil || !slices.Contains(sources, e.Source) || e.Attributes == nil {
			continue
		}
		cands = append(cands, candidate{e: e, key: readingKey(e)})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].e.Timestamp.After(cands[j].e.Timestamp) })
	newest, prior = map[string]*eventschema.Envelope{}, map[string]*eventschema.Envelope{}
	for _, c := range cands {
		if prior[c.key] != nil || !usable(c.e) {
			continue
		}
		if newest[c.key] == nil {
			newest[c.key] = c.e
		} else {
			prior[c.key] = c.e
		}
	}
	return newest, prior
}

// newestTwo must pick the same readings as the sort it replaced, for events
// in time order and out of it, with readings that share a timestamp.
func TestNewestTwoMatchesStableNewestFirst(t *testing.T) {
	base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	mk := func(min int, id, limit string) *eventschema.Envelope {
		return &eventschema.Envelope{
			ID: id, Source: "claude-code-statusline", Timestamp: base.Add(time.Duration(min) * time.Minute),
			Attributes: map[string]string{"limit": limit},
		}
	}
	usable := func(e *eventschema.Envelope) bool { return e.Attributes["limit"] != "" }
	sources := []string{"claude-code-statusline"}
	tables := map[string][]*eventschema.Envelope{
		"in order":                 {mk(0, "a", "x"), mk(1, "b", "x"), mk(2, "c", "y"), mk(3, "d", "x")},
		"out of order":             {mk(3, "d", "x"), mk(0, "a", "x"), mk(2, "c", "y"), mk(1, "b", "x")},
		"ties keep input order":    {mk(1, "b", "x"), mk(1, "a", "x"), mk(1, "c", "x"), mk(0, "z", "x")},
		"ties across limits":       {mk(2, "p", "x"), mk(2, "q", "y"), mk(2, "r", "x"), mk(1, "s", "y"), mk(2, "t", "y")},
		"unusable newest":          {mk(5, "n", ""), mk(4, "m", "x"), mk(3, "l", "x")},
		"nil entries":              {mk(0, "a", "x"), nil, mk(2, "c", "y"), nil, mk(2, "d", "y")},
		"nil entries out of order": {nil, mk(2, "c", "y"), mk(0, "a", "x"), nil, mk(1, "b", "x")},
	}
	for name, events := range tables {
		t.Run(name, func(t *testing.T) {
			gotN, gotP := newestTwo(events, sources, usable)
			wantN, wantP := newestTwoReference(events, sources, usable)
			if !sameReadings(gotN, wantN) || !sameReadings(gotP, wantP) {
				t.Errorf("newestTwo = %v / %v, want %v / %v", ids(gotN), ids(gotP), ids(wantN), ids(wantP))
			}
		})
	}
}

func sameReadings(a, b map[string]*eventschema.Envelope) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func ids(m map[string]*eventschema.Envelope) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v.ID
	}
	return out
}
