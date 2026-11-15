package sqlite

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A store written before migration 9 gains the older-changes counter when
// it is opened by a binary that knows it, and the counter then sees the
// changes the store already holds. The store is brought to version 8
// through the real migration path, given rows, then opened again.
func TestOlderChangesMigrationOnAStoredDatabase(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/events.db"
	now := time.Now().UTC()
	all := migrations

	migrations = all[:len(all)-1]
	s, err := Open(ctx, path, Options{})
	migrations = all
	if err != nil {
		t.Fatalf("open at version 8: %v", err)
	}
	for id, at := range map[string]time.Time{"young": now.Add(-time.Hour), "old": now.Add(-60 * 24 * time.Hour)} {
		env := mustPromptEnvelope(t, id, at, &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, InputTokens: 1})
		if err := s.Append(ctx, env); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s, err = Open(ctx, path, Options{})
	if err != nil {
		t.Fatalf("open at version 9: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var version int
	if err := s.DB().QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != all[len(all)-1].Version {
		t.Fatalf("schema version %d after open, want %d", version, all[len(all)-1].Version)
	}
	before := mustPosition(t, s)
	if _, err := s.DB().ExecContext(ctx, `UPDATE events SET source = 'other' WHERE id = 'old'`); err != nil {
		t.Fatalf("update old: %v", err)
	}
	afterOld := mustPosition(t, s)
	if afterOld.Older == before.Older {
		t.Errorf("update of an old event left Older at %d", before.Older)
	}
	if afterOld.Seq != before.Seq {
		t.Errorf("update of an old event logged a change: Seq %d -> %d", before.Seq, afterOld.Seq)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE events SET source = 'other' WHERE id = 'young'`); err != nil {
		t.Fatalf("update young: %v", err)
	}
	afterYoung := mustPosition(t, s)
	if afterYoung.Seq == afterOld.Seq {
		t.Errorf("update of a young event left Seq at %d", afterOld.Seq)
	}
	if afterYoung.Older != afterOld.Older {
		t.Errorf("update of a young event moved Older: %d -> %d", afterOld.Older, afterYoung.Older)
	}
}
