// Package derived decides when a figure derived from the event log has to
// be computed again (ADR 0019): when its inputs moved, when a boundary it
// shows passed, or when it reached its max age. In between it is answered
// as computed, and only what moves with the clock alone drifts.
package derived

import (
	"reflect"
	"time"
)

// Keep reports whether a result computed at at from inputs was, good
// until until, still answers for now with inputs at is: the inputs are
// known and have not moved, and now lies in [at, until).
func Keep(now, at, until time.Time, was, is string, known bool) bool {
	return known && was == is && !now.Before(at) && now.Before(until)
}

// Until is the latest moment a result computed at at stays what a
// computation would give with no input moved, but for time-only drift:
// the earliest of every exported time the values hold that lies ahead of
// at (a reset, a freshness limit, an expiry), each of extra that does,
// the next midnight here or in UTC (figures for today), and at plus
// maxAge.
func Until(at time.Time, maxAge time.Duration, extra []time.Time, values ...any) time.Time {
	until := at.Add(maxAge)
	sooner := func(t time.Time) {
		if t.After(at) && t.Before(until) {
			until = t
		}
	}
	for _, v := range values {
		walkTimes(reflect.ValueOf(v), sooner)
	}
	for _, t := range extra {
		sooner(t)
	}
	for _, loc := range []*time.Location{time.UTC, time.Local} {
		y, m, d := at.In(loc).Date()
		sooner(time.Date(y, m, d+1, 0, 0, 0, 0, loc))
	}
	return until
}

var timeType = reflect.TypeFor[time.Time]()

// walkTimes calls fn with every exported time.Time v holds, through
// pointers, interfaces, structs, slices, arrays and map values.
func walkTimes(v reflect.Value, fn func(time.Time)) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			walkTimes(v.Elem(), fn)
		}
	case reflect.Struct:
		if v.Type() == timeType {
			if v.CanInterface() {
				fn(v.Interface().(time.Time))
			}
			return
		}
		for i := range v.NumField() {
			walkTimes(v.Field(i), fn)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			walkTimes(v.Index(i), fn)
		}
	case reflect.Map:
		it := v.MapRange()
		for it.Next() {
			walkTimes(it.Value(), fn)
		}
	}
}
