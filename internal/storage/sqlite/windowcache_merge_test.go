package sqlite

import (
	"cmp"
	"context"
	"fmt"
	"math/rand"
	"slices"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// mergeRows gives what sorting the held and fresh rows together gives:
// fresh rows after, before and among the held ones, in order or not.
func TestMergeRowsSortsFreshRowsIn(t *testing.T) {
	type row struct{ at, id int }
	compare := func(a, b row) int { return cmp.Or(cmp.Compare(a.at, b.at), cmp.Compare(a.id, b.id)) }
	rng := rand.New(rand.NewSource(5))
	id := 0
	rows := func(n, from, span int) []row {
		out := make([]row, n)
		for i := range out {
			id++
			out[i] = row{at: from + rng.Intn(span+1), id: id}
		}
		return out
	}
	for i := range 500 {
		held := rows(rng.Intn(40), 0, 100)
		slices.SortFunc(held, compare)
		var fresh []row
		switch i % 4 {
		case 0: // the newest events, in order
			fresh = rows(rng.Intn(5), 100, 50)
			slices.SortFunc(fresh, compare)
		case 1: // late, anywhere
			fresh = rows(rng.Intn(5), 0, 150)
		case 2: // at the same instants as held rows
			for _, h := range held[:min(3, len(held))] {
				id++
				fresh = append(fresh, row{at: h.at, id: id})
			}
		case 3: // none
		}
		want := slices.Concat(held, fresh)
		slices.SortFunc(want, compare)
		got := mergeRows(slices.Clone(held), slices.Clone(fresh), compare)
		if !slices.Equal(got, want) {
			t.Fatalf("case %d: merged %v, sorted %v", i, got, want)
		}
	}
}

// One event stored late into a month of cached events, then read: the
// update the glance's memo makes while a source writes behind the clock.
func BenchmarkEventCacheLateEvent(b *testing.B) {
	ctx := context.Background()
	s, err := Open(ctx, b.TempDir()+"/events.db", Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	const n = 100_000
	envs := make([]*eventschema.Envelope, 0, n)
	for i := range n {
		envs = append(envs, promptAt(fmt.Sprintf("e%06d", i), cacheNow.Add(-time.Duration(i)*(30*24*time.Hour/n)), eventschema.ProviderAnthropic, 10))
	}
	if err := s.AppendBatch(ctx, envs); err != nil {
		b.Fatal(err)
	}
	c := NewEventCache(s, 32*24*time.Hour, time.Hour)
	c.now = func() time.Time { return cacheNow }
	since := cacheNow.Add(-31 * 24 * time.Hour)
	if _, err := c.ReadEvents(ctx, eventschema.EventTypePrompt, since); err != nil {
		b.Fatal(err)
	}
	i := 0
	b.ReportAllocs()
	for b.Loop() {
		i++
		late := promptAt(fmt.Sprintf("late%06d", i), cacheNow.Add(-time.Duration(i%1000)*time.Hour), eventschema.ProviderAnthropic, 10)
		if err := s.Append(ctx, late); err != nil {
			b.Fatal(err)
		}
		if _, err := c.ReadEvents(ctx, eventschema.EventTypePrompt, since); err != nil {
			b.Fatal(err)
		}
	}
}

// A month of cached events with one late event sorted in: sorting the
// whole window again, as the cache did, against merging the new row in.
// Copying the window for each round is left out of the timing.
func BenchmarkMergeOneLateRow(b *testing.B) {
	const n = 200_000
	base := cacheNow.Add(-30 * 24 * time.Hour)
	held := make([]cachedEvent, n)
	for i := range held {
		held[i] = cachedEvent{rowid: int64(i + 1), ev: &packedEvent{
			id: fmt.Sprintf("e%06d", i), ts: base.Add(time.Duration(i) * (30 * 24 * time.Hour / n)).UnixNano()}}
	}
	late := cachedEvent{rowid: n + 1, ev: &packedEvent{id: "late", ts: base.Add(15 * 24 * time.Hour).UnixNano()}}
	b.Run("sort the window", func(b *testing.B) {
		for b.Loop() {
			b.StopTimer()
			rows := append(slices.Clone(held), late)
			b.StartTimer()
			slices.SortFunc(rows, compareCached)
		}
	})
	b.Run("merge", func(b *testing.B) {
		for b.Loop() {
			b.StopTimer()
			// Room for the row, as a window's array mostly has.
			rows := slices.Grow(slices.Clone(held), 1)
			b.StartTimer()
			_ = mergeRows(rows, []cachedEvent{late}, compareCached)
		}
	})
}
