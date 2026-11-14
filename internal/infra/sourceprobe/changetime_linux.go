package sourceprobe

import (
	"io/fs"
	"syscall"
	"time"
)

// hasChangeTime reports whether changeTime reads a real change time here.
const hasChangeTime = true

// changeTime is the inode change time, which moves with every entry change
// and with every attempt to set the modification time back.
func changeTime(info fs.FileInfo) time.Time {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}
	}
	return time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec)) //nolint:unconvert // int32 on 32-bit arches
}
