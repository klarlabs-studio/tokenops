//go:build !darwin && !linux

package sourceprobe

import (
	"io/fs"
	"time"
)

// hasChangeTime reports whether changeTime reads a real change time here.
// Without one, a directory's modification time and identity are what say
// it changed, and the full re-walk covers a time set back by hand.
const hasChangeTime = false

func changeTime(fs.FileInfo) time.Time { return time.Time{} }
