package agentdx

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

// TranscriptCache keeps what each transcript file yielded, so a repeated
// scan re-reads only what changed. The daemon analyses the same week of
// transcripts every few hours, and reading all of them again took a
// minute or more of a core each time.
//
// A Claude Code transcript is read line by line, each line on its own, so
// a file that grew is read from where the last scan stopped. A Codex
// rollout carries state from line to line (the model of the turn, the
// context size before it), so a changed rollout is read again whole. An
// entry is used only when reading the file again would yield exactly what
// it holds; anything else reads the file.
//
// The cache holds no prompt text: a scan that asks for it reads the files
// as before (ExtractOptions.WithPromptText), so the words still live only
// for the length of a scan. A TranscriptCache is safe for concurrent use.
type TranscriptCache struct {
	mu     sync.Mutex
	claude map[string]*claudeEntry
	codexs map[string]*codexEntry
}

// NewTranscriptCache returns an empty cache.
func NewTranscriptCache() *TranscriptCache {
	return &TranscriptCache{claude: map[string]*claudeEntry{}, codexs: map[string]*codexEntry{}}
}

type cacheSource int

const (
	sourceClaudeCode cacheSource = iota
	sourceCodex
)

// fileStamp is what says a file is unchanged.
type fileStamp struct {
	size  int64
	mtime time.Time
}

func stampOf(info os.FileInfo) fileStamp {
	return fileStamp{size: info.Size(), mtime: info.ModTime()}
}

// claudeEntry is one Claude Code transcript as far as it has been read.
type claudeEntry struct {
	stamp fileStamp
	// since is the cut-off the records were read with.
	since time.Time
	// records come from the newline-terminated lines before offset.
	records []Record
	offset  int64
	// tail comes from a final line still being written; it is read again
	// once the file grows.
	tail []Record
	// stopped is set when a line too long to read ended the scan, as it
	// ends a whole-file read: nothing after it is ever read.
	stopped bool
}

// codexEntry is one Codex rollout, read whole.
type codexEntry struct {
	stamp   fileStamp
	since   time.Time
	first   time.Time
	records []Record
}

// writtenSince stats path and reports whether it can hold an entry at or
// after since. Each line is stamped when it is written, so a file last
// written before since has none; skipping it changes nothing but the
// time the scan takes.
func writtenSince(path string, since time.Time) (os.FileInfo, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, false
	}
	if !since.IsZero() && info.ModTime().Before(since) {
		return nil, false
	}
	return info, true
}

// notBefore reports whether a is at or after b, a zero time being the
// earliest of all.
func notBefore(a, b time.Time) bool {
	if b.IsZero() {
		return true
	}
	return !a.IsZero() && !a.Before(b)
}

// claudeCode returns what the Claude Code transcript at path yields from
// since on, reading only what the cache does not already hold.
func (c *TranscriptCache) claudeCode(path string, info os.FileInfo, since time.Time) []Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp := stampOf(info)
	e := c.claude[path]
	switch {
	case e != nil && notBefore(since, e.since) && e.stamp == stamp:
		// Unchanged.
	case e != nil && notBefore(since, e.since) && stamp.size > e.stamp.size:
		// Grown: read from the last complete line.
		if !e.stopped {
			recs, offset, tail, stopped, err := readClaudeFrom(path, e.offset, e.since)
			if err != nil {
				delete(c.claude, path)
				return nil
			}
			e.records = append(e.records, recs...)
			e.offset, e.tail, e.stopped = offset, tail, stopped
		}
		e.stamp = stamp
	default:
		recs, offset, tail, stopped, err := readClaudeFrom(path, 0, since)
		if err != nil {
			delete(c.claude, path)
			return nil
		}
		e = &claudeEntry{stamp: stamp, since: since, records: recs, offset: offset, tail: tail, stopped: stopped}
		c.claude[path] = e
	}
	// The cut-off only moves forward between the daemon's scans; records
	// before it can never be returned again.
	e.records = dropBefore(e.records, since)
	e.tail = dropBefore(e.tail, since)
	e.since = since
	out := make([]Record, 0, len(e.records)+len(e.tail))
	out = append(out, e.records...)
	return append(out, e.tail...)
}

// codex returns what the Codex rollout at path yields from since on,
// reading it again unless the cached read is exactly what it would yield.
func (c *TranscriptCache) codex(path string, info os.FileInfo, since time.Time) []Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp := stampOf(info)
	e := c.codexs[path]
	// The same file read with another cut-off yields the same records when
	// no line falls between the two: every line is at or after both.
	same := e != nil && e.stamp == stamp &&
		(e.since.Equal(since) || e.first.IsZero() || (notBefore(e.first, since) && notBefore(e.first, e.since)))
	if !same {
		f, err := os.Open(path) //nolint:gosec // operator's own transcript dir
		if err != nil {
			delete(c.codexs, path)
			return nil
		}
		recs, first := readCodexTranscriptFrom(f, sessionIDFromPath(path), since, false)
		_ = f.Close()
		e = &codexEntry{stamp: stamp, since: since, first: first, records: recs}
		c.codexs[path] = e
	}
	return append([]Record(nil), e.records...)
}

// retain forgets every file of src a scan did not keep: deleted, or last
// written before the window.
func (c *TranscriptCache) retain(src cacheSource, kept map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch src {
	case sourceClaudeCode:
		for p := range c.claude {
			if !kept[p] {
				delete(c.claude, p)
			}
		}
	case sourceCodex:
		for p := range c.codexs {
			if !kept[p] {
				delete(c.codexs, p)
			}
		}
	}
}

// Len reports how many files the cache holds.
func (c *TranscriptCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.claude) + len(c.codexs)
}

// dropBefore keeps the records at or after since, in their order. Records
// are in file order, which is not always time order, so every one is
// checked.
func dropBefore(recs []Record, since time.Time) []Record {
	if since.IsZero() {
		return recs
	}
	kept := recs[:0]
	for _, r := range recs {
		if !r.At.Before(since) {
			kept = append(kept, r)
		}
	}
	return kept
}

// readClaudeFrom reads a Claude Code transcript from offset, which is the
// start of a line. It returns the records of the newline-terminated lines,
// the offset after the last of them, the records of an unterminated final
// line, and whether a line too long to read ended the scan.
func readClaudeFrom(path string, offset int64, since time.Time) (recs []Record, end int64, tail []Record, stopped bool, err error) {
	f, err := os.Open(path) //nolint:gosec // operator's own transcript dir
	if err != nil {
		return nil, 0, nil, false, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, 0, nil, false, err
	}
	r := bufio.NewReaderSize(f, 1<<20)
	end = offset
	var long []byte
	for {
		chunk, rerr := r.ReadSlice('\n')
		line := chunk
		if errors.Is(rerr, bufio.ErrBufferFull) || long != nil {
			long = append(long, chunk...)
			line = long
		}
		if len(trimLine(line)) >= maxTranscriptLine {
			return recs, end, nil, true, nil
		}
		if errors.Is(rerr, bufio.ErrBufferFull) {
			continue
		}
		if rerr == nil {
			end += int64(len(line))
			recs = appendTranscriptLine(recs, trimLine(line), since, false)
			long = nil
			continue
		}
		if !errors.Is(rerr, io.EOF) {
			return nil, 0, nil, false, rerr
		}
		if len(line) > 0 {
			tail = appendTranscriptLine(nil, trimLine(line), since, false)
		}
		return recs, end, tail, false, nil
	}
}

// trimLine drops a line's newline and a carriage return before it, as
// bufio.ScanLines does.
func trimLine(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("\n"))
	return bytes.TrimSuffix(b, []byte("\r"))
}
