package pricing

import "errors"

// ErrSnapshotNotFound is returned by FindSnapshot when a specific selector
// (a timestamp) matches no persisted snapshot. Callers use errors.Is to
// distinguish "you asked for a snapshot that doesn't exist" from an I/O
// failure.
var ErrSnapshotNotFound = errors.New("pricing: snapshot not found")

// ErrFetch wraps any failure to fetch or parse a Source. Refresh uses it to
// exit non-zero without writing, while keeping the offline baseline intact.
var ErrFetch = errors.New("pricing: source fetch failed")
