package accounts

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/heartbeat"
)

// An account nobody is using reports the same reading on every scan. It
// was stored once, so nothing in the store told a reader that works from
// one that stopped. Each source's unchanged reading is stored again every
// heartbeat, under a new ID and time, and says for how long it stands; a
// reading the store refused is offered again on the next scan.
func TestPollerKeepsEachSourcesUnchangedReadingCurrent(t *testing.T) {
	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	at := start
	steady := &fakeReader{endpoint: "openrouter", good: "k", reading: Reading{Scope: "key", UsedUSD: 4, HasUsed: true}}
	moving := &fakeReader{endpoint: "deepseek", good: "k", reading: Reading{HasBalance: true, BalanceUSD: 20}}
	bus := &captureBus{refuse: 1}
	p := NewPoller(bus, PollerOptions{
		Readers:  []Reader{steady, moving},
		Interval: 5 * time.Minute,
		Credentials: func() []Credential {
			return []Credential{{Endpoint: "openrouter", Key: "k"}, {Endpoint: "deepseek", Key: "k"}}
		},
		Now: func() time.Time { return at },
	})

	stored := map[string][]time.Duration{}
	seen := 0
	for at = start; at.Before(start.Add(26 * time.Minute)); at = at.Add(5 * time.Minute) {
		// The balance moves on every scan; the spend never does.
		moving.reading.BalanceUSD--
		p.Scan(context.Background())
		for _, env := range bus.got[seen:] {
			stored[env.Source] = append(stored[env.Source], at.Sub(start))
		}
		seen = len(bus.got)
	}
	// The first scan's first reading was refused, so the steady source is
	// first stored a scan later, and then every second scan.
	if got := stored["openrouter-account"]; len(got) != 3 || got[0] != 5*time.Minute || got[1] != 15*time.Minute || got[2] != 25*time.Minute {
		t.Errorf("the unchanged reading was stored at %v, want 5m, 15m and 25m", got)
	}
	if got := stored["deepseek-account"]; len(got) != 6 {
		t.Errorf("the changing reading was stored %d times in 6 scans: %v", len(got), got)
	}
	ids := map[string]bool{}
	first := map[string]bool{}
	for i, env := range bus.got {
		if d, ok := heartbeat.FreshFor(env.Attributes); !ok || d != 25*time.Minute {
			t.Errorf("reading %d (%s) stands for %s (%v), want 25m", i, env.Source, d, ok)
		}
		// Only the steady source repeats a reading, and not its first.
		wantRepeat := env.Source == "openrouter-account" && first[env.Source]
		if got := heartbeat.IsRepeat(env.Attributes); got != wantRepeat {
			t.Errorf("reading %d (%s): repeat = %v, want %v", i, env.Source, got, wantRepeat)
		}
		first[env.Source] = true
		ids[env.ID] = true
	}
	if len(ids) != len(bus.got) {
		t.Errorf("%d IDs for %d readings: the store keeps the first of each ID", len(ids), len(bus.got))
	}
}

// A paced reader is read hours apart, so its reading stands for as long
// as that takes: judged by the scan interval it would be stale between
// two reads of a reader that works.
func TestAPacedReadersReadingStandsUntilItsNextReads(t *testing.T) {
	r := &pacedReader{fakeReader: fakeReader{endpoint: "aws", good: "k", reading: Reading{HasUsed: true, UsedUSD: 1}}, every: 8 * time.Hour}
	start := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	at := start
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		Readers:     []Reader{r},
		Credentials: func() []Credential { return []Credential{{Endpoint: "aws", Key: "k"}} },
		Now:         func() time.Time { return at },
	})
	for at = start; at.Before(start.Add(17 * time.Hour)); at = at.Add(15 * time.Minute) {
		p.Scan(context.Background())
	}
	if len(bus.got) != 3 {
		t.Fatalf("stored %d readings in 17 hours of an 8-hour reader, want 3", len(bus.got))
	}
	for i, env := range bus.got {
		limit, ok := heartbeat.FreshFor(env.Attributes)
		if !ok {
			t.Fatalf("reading %d does not say how long it stands", i)
		}
		if i > 0 {
			if gap := env.Timestamp.Sub(bus.got[i-1].Timestamp); gap >= limit {
				t.Errorf("readings %d and %d are %s apart and the first stands for %s", i-1, i, gap, limit)
			}
		}
		if limit < 2*8*time.Hour || limit > 26*time.Hour {
			t.Errorf("reading %d stands for %s: want two missed reads and no more than a day or so", i, limit)
		}
	}
}
