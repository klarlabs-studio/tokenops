package readguard

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"time"
)

// compactBoundary is the subtype of the entry Claude Code writes to a
// session's transcript when it compacts the conversation.
var compactBoundary = []byte(`"compact_boundary"`)

// ForgetCompacted forgets the main agent's reads that a compaction has
// since dropped from its context. After /compact the agent no longer
// holds the files it read, so refusing a re-read would leave it without
// them. It reads only what transcriptPath gained since the last call and
// fails open: an unreadable transcript forgets nothing.
//
// It is called by the read hook rather than a compaction hook, so it
// works where only the Read hook is installed. A subagent compacts its
// own context, not the session's; its reads are left alone.
func ForgetCompacted(dir, sessionID, transcriptPath string) {
	if transcriptPath == "" {
		return
	}
	dir = resolveDir(dir)
	st := loadSession(dir, sessionID)
	at, end, ok := lastCompaction(transcriptPath, st.TranscriptOffset)
	if !ok {
		return
	}
	st.TranscriptOffset = end
	if !at.IsZero() {
		for path, ps := range st.Paths {
			if ps.ReadAt < at.UnixNano() {
				delete(st.Paths, path)
			}
		}
	}
	saveSession(dir, sessionID, st)
}

// lastCompaction reads transcriptPath's complete lines from offset on and
// returns the time of the newest compaction among them (zero for none)
// and the offset just past the last complete line. A transcript shorter
// than offset was rewritten, and is read from the start.
func lastCompaction(transcriptPath string, offset int64) (time.Time, int64, bool) {
	f, err := os.Open(transcriptPath) //nolint:gosec // the hook's own session transcript
	if err != nil {
		return time.Time{}, 0, false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return time.Time{}, 0, false
	}
	if info.Size() < offset {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return time.Time{}, 0, false
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return time.Time{}, 0, false
	}
	// A half-written last line is read on the next call.
	complete := bytes.LastIndexByte(b, '\n') + 1
	var newest time.Time
	for _, line := range bytes.Split(b[:complete], []byte{'\n'}) {
		if !bytes.Contains(line, compactBoundary) {
			continue
		}
		var e struct {
			Subtype   string    `json:"subtype"`
			Timestamp time.Time `json:"timestamp"`
		}
		if json.Unmarshal(line, &e) == nil && e.Subtype == "compact_boundary" && e.Timestamp.After(newest) {
			newest = e.Timestamp
		}
	}
	return newest, offset + int64(complete), true
}
