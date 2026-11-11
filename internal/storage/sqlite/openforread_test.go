package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A command that only reads opens a current store read-only: it cannot
// write to it, and it reads what is there.
func TestOpenForReadOpensACurrentStoreReadOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.db")
	rw, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rw.Append(ctx, mustPromptEnvelope(t, "kept", time.Now(), &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic})); err != nil {
		t.Fatal(err)
	}
	_ = rw.Close()

	s, err := OpenForRead(ctx, path)
	if err != nil {
		t.Fatalf("OpenForRead: %v", err)
	}
	defer func() { _ = s.Close() }()
	got, err := s.ReadEvents(ctx, eventschema.EventTypePrompt, time.Time{})
	if err != nil || len(got) != 1 {
		t.Fatalf("read %d events, %v; want the one stored", len(got), err)
	}
	if err := s.Append(ctx, mustPromptEnvelope(t, "new", time.Now(), &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic})); err == nil {
		t.Error("a store opened for reading accepted a write")
	}
}

// A read does not make a store where there is none: the command says
// there is nothing recorded instead of answering from an empty one.
func TestOpenForReadDoesNotCreateAStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	_, err := OpenForRead(context.Background(), path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("OpenForRead of a missing store = %v, want fs.ErrNotExist", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("a read created %s", path)
	}
}

// A store an older binary wrote lacks the schema the reads expect: it is
// brought up to date first, as a read-write open does.
func TestOpenForReadMigratesAnOlderStore(t *testing.T) {
	ctx := context.Background()
	for name, seed := range map[string]func(t *testing.T, path string){
		"a migration missing": func(t *testing.T, path string) {
			// What the binary before the last migration wrote.
			all := migrations
			migrations = all[:len(all)-1]
			defer func() { migrations = all }()
			s, err := Open(ctx, path, Options{})
			if err != nil {
				t.Fatal(err)
			}
			_ = s.Close()
		},
		"no migration table": func(t *testing.T, path string) {
			db, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `CREATE TABLE other (x INTEGER)`); err != nil {
				t.Fatal(err)
			}
			_ = db.Close()
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "events.db")
			seed(t, path)
			s, err := OpenForRead(ctx, path)
			if err != nil {
				t.Fatalf("OpenForRead: %v", err)
			}
			defer func() { _ = s.Close() }()
			var n int
			if err := s.DB().QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n != len(migrations) {
				t.Errorf("%d of %d migrations applied (%v) after opening an older store; want it brought up to date", n, len(migrations), err)
			}
			if _, err := s.ReadEvents(ctx, eventschema.EventTypePrompt, time.Time{}); err != nil {
				t.Errorf("read after the upgrade: %v", err)
			}
		})
	}
}
