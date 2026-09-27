package daemon

import (
	"context"
	"hash/maphash"
	"log/slog"

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// ingestionBus is the bus pollers publish to: bus, minus envelopes the store
// already holds. If the snapshot cannot be read, pollers get bus itself —
// the store still deduplicates, so the cost is write load, not correctness.
func ingestionBus(ctx context.Context, bus events.Bus, store *sqlite.Store, logger *slog.Logger) events.Bus {
	if store == nil {
		return bus
	}
	known, n, err := loadStoredIDs(ctx, store)
	if err != nil {
		logger.Warn("stored event ids unavailable; pollers will re-publish history", "err", err)
		return bus
	}
	logger.Info("pollers skip stored events", "stored_ids", n)
	return events.SkipKnown(bus, known)
}

// loadStoredIDs snapshots the IDs already in the store so pollers can skip
// re-publishing them after a restart. IDs are kept as 64-bit hashes: poller
// envelope IDs are themselves 64-bit truncated SHA-256 digests, so the set
// adds no collision class the IDs do not already carry, at a fraction of
// the memory of holding the strings. The snapshot is taken once; envelopes
// stored later are deduplicated by each poller's own seen set.
func loadStoredIDs(ctx context.Context, store *sqlite.Store) (func(id string) bool, int, error) {
	seed := maphash.MakeSeed()
	set := make(map[uint64]struct{})
	if err := store.EachEventID(ctx, func(id string) {
		set[maphash.String(seed, id)] = struct{}{}
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
