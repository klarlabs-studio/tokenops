// Package filelock takes an exclusive advisory lock on an open file, so
// processes appending to the same ledger do not interleave writes.
package filelock

import "os"

// Lock blocks until it holds an exclusive lock on f and returns the
// function that releases it. The lock is advisory: it only excludes
// other callers of Lock.
func Lock(f *os.File) (func(), error) {
	if err := lock(f); err != nil {
		return nil, err
	}
	return func() { _ = release(f) }, nil
}
