package derived

import (
	"testing"
	"time"
)

// Keep answers the last result only while its inputs stand where they
// stood and now lies in [at, until).
func TestKeep(t *testing.T) {
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	until := at.Add(5 * time.Minute)
	cases := []struct {
		name    string
		now     time.Time
		was, is string
		known   bool
		want    bool
	}{
		{name: "nothing moved", now: at.Add(time.Minute), was: "cfg|10|3", is: "cfg|10|3", known: true, want: true},
		{name: "an event was added", now: at.Add(time.Minute), was: "cfg|10|3", is: "cfg|11|3", known: true},
		{name: "an event was updated or deleted", now: at.Add(time.Minute), was: "cfg|10|3", is: "cfg|10|4", known: true},
		{name: "the config changed", now: at.Add(time.Minute), was: "cfg|10|3", is: "cfg2|10|3", known: true},
		{name: "a boundary or the max age passed", now: until, was: "cfg|10|3", is: "cfg|10|3", known: true},
		{name: "just before it", now: until.Add(-time.Nanosecond), was: "cfg|10|3", is: "cfg|10|3", known: true, want: true},
		{name: "the inputs could not be read", now: at.Add(time.Minute), was: "cfg|10|3", known: false},
		{name: "the clock went back", now: at.Add(-time.Minute), was: "cfg|10|3", is: "cfg|10|3", known: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Keep(c.now, at, until, c.was, c.is, c.known); got != c.want {
				t.Errorf("Keep = %v, want %v", got, c.want)
			}
		})
	}
}

type window struct {
	ResetsAt time.Time
	Until    *time.Time
	hidden   time.Time
}

// Until is the earliest time ahead of at that the values hold or extra
// names, a midnight here or in UTC, or at plus maxAge.
func TestUntil(t *testing.T) {
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	soon := at.Add(2 * time.Minute)
	next := func(loc *time.Location) time.Time {
		y, m, d := at.In(loc).Date()
		return time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	}
	midnight := next(time.UTC)
	if local := next(time.Local); local.Before(midnight) {
		midnight = local
	}
	cases := []struct {
		name   string
		values []any
		extra  []time.Time
		maxAge time.Duration
		want   time.Time
	}{
		{name: "the max age when nothing comes sooner", maxAge: 5 * time.Minute, want: at.Add(5 * time.Minute)},
		{name: "a reset in a slice", values: []any{[]window{{ResetsAt: at.Add(time.Hour)}, {ResetsAt: soon}}},
			maxAge: 5 * time.Minute, want: soon},
		{name: "a time behind a pointer in a map", values: []any{map[string]window{"a": {Until: &soon}}},
			maxAge: 5 * time.Minute, want: soon},
		{name: "a past time is no boundary", values: []any{window{ResetsAt: at.Add(-time.Minute)}},
			maxAge: 5 * time.Minute, want: at.Add(5 * time.Minute)},
		{name: "an unexported time is not read", values: []any{window{hidden: soon}},
			maxAge: 5 * time.Minute, want: at.Add(5 * time.Minute)},
		{name: "an extra moment", extra: []time.Time{at.Add(-time.Hour), soon},
			maxAge: 5 * time.Minute, want: soon},
		{name: "a midnight before the max age", maxAge: 48 * time.Hour, want: midnight},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Until(at, c.maxAge, c.extra, c.values...); !got.Equal(c.want) {
				t.Errorf("Until = %s, want %s", got, c.want)
			}
		})
	}
}
