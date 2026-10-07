//go:build windows

package filelock

import (
	"math"
	"os"

	"golang.org/x/sys/windows"
)

// Locking the whole possible range is the LockFileEx analogue of flock.
func lock(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0,
		math.MaxUint32, math.MaxUint32, new(windows.Overlapped))
}

func release(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, math.MaxUint32, math.MaxUint32, new(windows.Overlapped))
}
