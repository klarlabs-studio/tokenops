package claudecodejsonl

import (
	"io"
	"os"
	"time"
)

// ReadTail reads the last maxBytes of a transcript: the assistant turns
// there, one per message (its first line, timed from the entry before
// it, so At minus Latency is when its request went out), and when the
// last entry was written. A line cut by the start of the tail is skipped.
// It is what a caller that runs every few seconds, such as the status
// line, can afford: a long session's transcript runs to many megabytes.
func ReadTail(path string, maxBytes int64) ([]Turn, time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, time.Time{}, err
	}
	if off := info.Size() - maxBytes; off > 0 {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return nil, time.Time{}, err
		}
	}
	var turns []Turn
	seen := map[string]bool{}
	st := &readState{}
	_, err = readLines(f, sourceOf(path), st, true, func(t Turn) error {
		if !seen[t.MessageID] {
			seen[t.MessageID] = true
			turns = append(turns, t)
		}
		return nil
	})
	return turns, st.prevAt.UTC(), err
}
