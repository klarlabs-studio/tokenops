package daemon

import (
	"context"
	"hash/maphash"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/lifecycle"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// loadStoredIDsFn is the snapshot read; tests replace it.
var loadStoredIDsFn = loadStoredIDs

// ingestionBus is the bus pollers publish to: bus, minus envelopes the store
// already holds.
//
// The snapshot of stored ids is read beside the boot, not before it. It
// was read before the port opened, and on a large store under start-up
// load (the corrections, the glance's warm-up and the pollers' own reads
// all start at once) it finished 31s, and on another restart 5m25s, after
// the store opened, while `daemon restart` waits 20s. Until it is in, a
// publish waits for it, so a poller replaying history still skips what
// is stored; a publish whose daemon is shutting down goes through. If the snapshot cannot be read, pollers get bus
// itself: the store still deduplicates, so the cost is write load, not
// correctness.
func ingestionBus(ctx context.Context, bus events.Bus, store *sqlite.Store, sup *lifecycle.Supervisor, logger *slog.Logger) events.Bus {
	if store == nil {
		return bus
	}
	snap := &storedIDSnapshot{done: make(chan struct{}), stop: ctx.Done()}
	sup.Go("stored-ids", func(ctx context.Context) error {
		t0 := time.Now()
		known, n, err := loadStoredIDsFn(ctx, store)
		snap.publish(known)
		if err != nil {
			logger.Warn("stored event ids unavailable; pollers will re-publish history", "err", err)
			return nil
		}
		logger.Info("pollers skip stored events", "stored_ids", n, "took", time.Since(t0).Round(time.Millisecond))
		return nil
	})
	return events.SkipKnown(bus, snap.known)
}

// storedIDSnapshot is the stored-id set once it has been read.
type storedIDSnapshot struct {
	done chan struct{}
	stop <-chan struct{}
	set  func(id string) bool
}

// publish installs the set (nil when it could not be read) and releases
// the publishes waiting for it.
func (s *storedIDSnapshot) publish(known func(id string) bool) {
	s.set = known
	close(s.done)
}

// known reports whether id is stored, waiting for the snapshot first.
func (s *storedIDSnapshot) known(id string) bool {
	select {
	case <-s.done:
	case <-s.stop:
		return false
	}
	return s.set != nil && s.set(id)
}

// loadStoredIDs snapshots the IDs already in the store so pollers can skip
// re-publishing them after a restart. IDs are kept as 64-bit hashes: poller
// envelope IDs are themselves 64-bit truncated SHA-256 digests, so the set
// adds no collision class the IDs do not already carry, at a fraction of
// the memory of holding the strings. The ids are hashed straight from the
// driver's buffer, so reading them allocates little beyond the set. The
// snapshot is taken once; envelopes stored later are deduplicated by each
// poller's own seen set.
//
// It reads over a connection of its own (see snapshotReader), so it never
// queues behind the boot's other readers for the store's shared pool.
func loadStoredIDs(ctx context.Context, store *sqlite.Store) (func(id string) bool, int, error) {
	reader, release := snapshotReader(ctx, store)
	defer release()
	seed := maphash.MakeSeed()
	set := make(map[uint64]struct{})
	if err := reader.EachEventID(ctx, func(id []byte) {
		set[maphash.Bytes(seed, id)] = struct{}{}
	}); err != nil {
		return nil, 0, err
	}
	known := func(id string) bool {
		if id == "" {
			return false
		}
		_, ok := set[maphash.String(seed, id)]
		return ok
	}
	return known, len(set), nil
}

// snapshotReader opens a read-only handle on store's file for the
// snapshot. The store's pool holds four connections, and at start the
// glance's warm-up, the corrections, the boundary figures and the
// freshness reads all want one, so on a loaded machine the snapshot can
// queue behind them; one restart logged it 5m25s after the store opened,
// although reading 320k ids costs about 130ms of CPU. In WAL mode a
// reader never waits for the writer, so its own connection makes it
// independent of everything else the boot reads. An in-memory store, or
// a file that cannot be opened read-only, is read through the store.
func snapshotReader(ctx context.Context, store *sqlite.Store) (*sqlite.Store, func()) {
	if p := store.Path(); p != "" && p != ":memory:" {
		if ro, err := sqlite.OpenReadOnly(ctx, p); err == nil {
			return ro, func() { _ = ro.Close() }
		}
	}
	return store, func() {}
}
