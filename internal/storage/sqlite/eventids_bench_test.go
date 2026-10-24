package sqlite_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/internal/storage/sqlite/sqlitetest"
)

// storedIDsBenchEvents is the size of the store the stored-id benchmark
// reads: about what a daemon that has run for months holds (317k ids
// were loaded on a real restart). TOKENOPS_BENCH_EVENTS overrides it.
const storedIDsBenchEvents = 320_000

// BenchmarkEachEventID times reading every stored id, the snapshot the
// daemon's pollers dedupe against after a restart.
func BenchmarkEachEventID(b *testing.B) {
	n := storedIDsBenchEvents
	if v := os.Getenv("TOKENOPS_BENCH_EVENTS"); v != "" {
		if _, err := fmt.Sscan(v, &n); err != nil {
			b.Fatalf("TOKENOPS_BENCH_EVENTS: %v", err)
		}
	}
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(b.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	sqlitetest.Fill(b, s, n)
	b.ResetTimer()
	for b.Loop() {
		got := 0
		if err := s.EachEventID(ctx, func([]byte) { got++ }); err != nil {
			b.Fatal(err)
		}
		if got != n {
			b.Fatalf("ids = %d, want %d", got, n)
		}
	}
}
