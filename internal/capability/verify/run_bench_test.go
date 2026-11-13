package verify_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/verify"
	"go.klarlabs.de/tokenops/internal/contexts/work"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// BenchmarkCompareStored reads a 30-day period of about 411,000 events, the
// size of a busy month, of which a few hundred can bear on twenty attempts:
// the rest are unlinked prompts with no actor or from other sessions. The
// store is seeded once, outside the timer. Run with -benchtime=3x; it is
// skipped under -short.
func BenchmarkCompareStored(b *testing.B) {
	if testing.Short() {
		b.Skip("seeds a month of events")
	}
	const (
		period   = 411_000
		attempts = 20
	)
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(b.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })

	start := t0
	execs := make([]work.Execution, 0, attempts)
	for k := range attempts {
		execs = append(execs, execution(fmt.Sprintf("e%d", k), work.ActorID(fmt.Sprintf("session:bench-%d", k)),
			start.Add(time.Duration(k)*time.Hour), 30*time.Minute))
	}
	events := make([]*eventschema.Envelope, 0, period)
	for i := range period {
		at := start.Add(time.Duration(i) * 6 * time.Second)
		var env *eventschema.Envelope
		switch {
		case i%5000 == 0:
			k := (i / 5000) % attempts
			env = promptEvent(fmt.Sprintf("session:bench-%d", k), start.Add(time.Duration(k)*time.Hour+time.Minute), 100)
			env.Association.Execution = fmt.Sprintf("e%d", k)
		case i%2000 == 1:
			k := (i / 2000) % attempts
			env = promptEvent(fmt.Sprintf("session:bench-%d", k), start.Add(time.Duration(k)*time.Hour+2*time.Minute), 50)
		case i%3 == 0:
			env = promptEvent("", at, 10)
		default:
			env = promptEvent(fmt.Sprintf("session:noise-%d", i%50), at, 10)
		}
		env.ID = fmt.Sprintf("bench-%07d", i)
		env.Timestamp = at
		events = append(events, env)
	}
	if err := store.AppendBatch(ctx, events); err != nil {
		b.Fatal(err)
	}
	rs := reconstructed(execs)
	since := start.Add(-time.Hour)

	b.ResetTimer()
	for range b.N {
		if _, err := verify.CompareStored(ctx, store, rs, since, ""); err != nil {
			b.Fatal(err)
		}
	}
}
