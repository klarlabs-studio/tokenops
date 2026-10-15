package avoidable

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// The week's report reads five weeks of turns, every workflow of the
// week and git history, so the glance does not compute it: the daemon
// runs it in the background once a day and keeps it here, and the
// findings show its headline.

// Snapshot is the week's report as of when it ran.
type Snapshot struct {
	ComputedAt time.Time `json:"computed_at"`
	Report     Report    `json:"report"`
}

// MaxSnapshotAge is how old a snapshot the glance still shows: a day's
// refresh with a missed run to spare.
const MaxSnapshotAge = 2 * 24 * time.Hour

const snapshotFile = "avoidable-week.json"

// Run computes the last DefaultDays and stores the snapshot in dir.
func Run(ctx context.Context, d Deps, dir string, now time.Time) (Snapshot, error) {
	r, err := Compute(ctx, d, now.AddDate(0, 0, -DefaultDays), now)
	if err != nil {
		return Snapshot{}, err
	}
	s := Snapshot{ComputedAt: now.UTC(), Report: r}
	return s, WriteSnapshot(dir, s)
}

// ReadSnapshot reads the last run; nil when none has run.
func ReadSnapshot(dir string) (*Snapshot, error) {
	b, err := os.ReadFile(filepath.Join(dir, snapshotFile)) //nolint:gosec // TokenOps' own cache directory
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// WriteSnapshot stores s in one rename, so a reader never sees half.
func WriteSnapshot(dir string, s Snapshot) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, snapshotFile+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, snapshotFile))
}
