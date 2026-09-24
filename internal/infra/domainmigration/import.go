// Package domainmigration imports the former domain-events JSONL stream into
// the canonical SQLite event ledger. It exists only for the Phase 6 transition.
package domainmigration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"go.klarlabs.de/tokenops/internal/domainevents"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

const importBatchSize = 1000

// Result reports how much legacy history was read and copied.
type Result struct {
	Read       int
	Imported   int
	Duplicates int
	Skipped    int
}

// Import copies rotated JSONL history, oldest first, into the canonical store.
// Stable IDs make retries idempotent. Content matching also avoids duplicating
// records already dual-written by the live Phase 6 bridge.
func Import(ctx context.Context, store *sqlite.Store, path string, maxBackups int) (Result, error) {
	if store == nil {
		return Result{}, errors.New("domainmigration: store is nil")
	}
	if path == "" {
		return Result{}, errors.New("domainmigration: path is empty")
	}
	if maxBackups < 0 {
		maxBackups = 0
	}
	existing, err := store.Query(ctx, sqlite.Filter{Type: eventschema.EventTypeDomain, Limit: 1_000_000})
	if err != nil {
		return Result{}, fmt.Errorf("domainmigration: query canonical events: %w", err)
	}
	ids := make(map[string]struct{}, len(existing))
	contents := make(map[string]struct{}, len(existing))
	for _, env := range existing {
		ids[env.ID] = struct{}{}
		if p, ok := env.Payload.(*eventschema.DomainEvent); ok {
			contents[contentKey(p.Kind, p.Data)] = struct{}{}
		}
	}

	paths := make([]string, 0, maxBackups+1)
	for suffix := maxBackups; suffix >= 1; suffix-- {
		paths = append(paths, fmt.Sprintf("%s.%d", path, suffix))
	}
	paths = append(paths, path)
	var result Result
	batch := make([]*eventschema.Envelope, 0, importBatchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := store.AppendBatch(ctx, batch); err != nil {
			return fmt.Errorf("domainmigration: append canonical batch: %w", err)
		}
		batch = batch[:0]
		return nil
	}
	for _, source := range paths {
		if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return result, fmt.Errorf("domainmigration: stat %s: %w", source, err)
		}
		skipped, err := domainevents.ReplayLenient(source, func(rec domainevents.Record) error {
			result.Read++
			env, err := domainevents.EnvelopeFromRecord(rec)
			if err != nil {
				return err
			}
			payload := env.Payload.(*eventschema.DomainEvent)
			key := contentKey(payload.Kind, payload.Data)
			if _, ok := ids[env.ID]; ok {
				result.Duplicates++
				return nil
			}
			if _, ok := contents[key]; ok {
				result.Duplicates++
				return nil
			}
			ids[env.ID] = struct{}{}
			contents[key] = struct{}{}
			batch = append(batch, env)
			result.Imported++
			if len(batch) >= importBatchSize {
				return flush()
			}
			return nil
		})
		result.Skipped += skipped
		if err != nil {
			return result, fmt.Errorf("domainmigration: read %s: %w", source, err)
		}
	}
	if err := flush(); err != nil {
		return result, err
	}
	return result, nil
}

func contentKey(kind string, data json.RawMessage) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return kind + "\x00" + string(data)
	}
	return kind + "\x00" + compact.String()
}
