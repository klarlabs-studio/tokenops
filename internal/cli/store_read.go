package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// openStoreForRead opens the event store at path for a command that only
// reads it: read-only, so the read takes no write lock from the daemon,
// and without making a store where there is none. Opened read-write, a
// read created an empty store and answered from it as if nothing had
// happened, and ran the migrations every time it was asked.
//
// A missing store is a *noStoreError, which satisfies
// errors.Is(err, fs.ErrNotExist).
func openStoreForRead(ctx context.Context, path string) (*sqlite.Store, error) {
	s, err := sqlite.OpenForRead(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, &noStoreError{path: path}
	}
	return s, err
}

// noStoreError is an event store that does not exist yet.
type noStoreError struct{ path string }

func (e *noStoreError) Error() string {
	return fmt.Sprintf("no event store at %s: nothing has been recorded there yet; %s", e.path, restartCommand(false))
}

func (e *noStoreError) Unwrap() error { return fs.ErrNotExist }

// nothingRecorded counts the events of a store that does not exist: none,
// from every source.
type nothingRecorded struct{}

func (nothingRecorded) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return map[string]int64{}, nil
}
