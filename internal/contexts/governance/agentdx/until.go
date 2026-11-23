package agentdx

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A scan bounded by Until reads only the transcripts that can hold an
// entry before it: those begun before it. A week's figures computed weeks
// later then read that week's files, not every file written since.
//
// When a transcript began is known without reading it: a Codex rollout
// names its start, and a Claude Code transcript's first line carries it.
// startSkew allows for what neither says exactly: the rollout's name is
// in local time, and a Claude Code entry may be stamped a little before
// the one above it (by up to half an hour on one real machine).
const startSkew = 24 * time.Hour

// startPeekLines and startPeekBytes bound the read for a Claude Code
// transcript's first stamped line; one not found within them is read.
const (
	startPeekLines = 32
	startPeekBytes = 4 << 20
)

// begunAfter reports whether the transcript at path began after until,
// so it holds nothing before it. Unknown starts are read.
func begunAfter(path string, until time.Time, codex bool) bool {
	if until.IsZero() {
		return false
	}
	start, ok := time.Time{}, false
	if codex {
		start, ok = rolloutStart(path)
	} else {
		start, ok = transcriptStart(path)
	}
	return ok && !start.Before(until.Add(startSkew))
}

// rolloutStart is the start a Codex rollout's name carries:
// rollout-2006-01-02T15-04-05-<id>.jsonl[.zst], read as UTC.
func rolloutStart(path string) (time.Time, bool) {
	name := strings.TrimPrefix(filepath.Base(path), "rollout-")
	const layout = "2006-01-02T15-04-05"
	if len(name) < len(layout) {
		return time.Time{}, false
	}
	t, err := time.Parse(layout, name[:len(layout)])
	return t, err == nil
}

// transcriptStart is the time on a Claude Code transcript's first stamped
// line.
func transcriptStart(path string) (time.Time, bool) {
	f, err := os.Open(path) //nolint:gosec // operator's own transcript dir
	if err != nil {
		return time.Time{}, false
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReaderSize(io.LimitReader(f, startPeekBytes), 64<<10)
	for range startPeekLines {
		line, err := br.ReadBytes('\n')
		var e struct {
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(line, &e) == nil && e.Timestamp != "" {
			if t, perr := time.Parse(time.RFC3339Nano, e.Timestamp); perr == nil {
				return t.UTC(), true
			}
		}
		if err != nil {
			// The end of the file or of the peek, or a read error.
			return time.Time{}, false
		}
	}
	return time.Time{}, false
}

// before keeps the records before until, in place; a zero until keeps
// every one. recs must be the caller's own slice.
func before(recs []Record, until time.Time) []Record {
	if until.IsZero() {
		return recs
	}
	kept := recs[:0]
	for _, r := range recs {
		if r.At.Before(until) {
			kept = append(kept, r)
		}
	}
	return kept
}
