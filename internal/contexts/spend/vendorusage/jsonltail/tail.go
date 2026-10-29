// Package jsonltail reads a set of append-only JSONL files incrementally:
// each scan parses only the bytes appended since the last one, carrying the
// caller's per-file reader state across the gap.
//
// The transcript pollers used to re-read every file from byte zero on every
// tick. On a real install that was ~1.4 GB of Claude Code transcripts parsed
// every 30s, which held a core near 90% indefinitely and starved the event
// store's writes into context-deadline retries.
package jsonltail

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"time"
)

// headBytes is how much of a file's start identifies it. A rewrite that
// drops or edits its first lines changes these bytes, so the file is read
// again from the start instead of from an offset into the new content.
const headBytes = 256

// MaxLineBytes bounds one line. Transcript lines can be large (full
// conversation history), but a line past this is skipped rather than held
// in memory — and skipped alone, so the lines after it are still read.
const MaxLineBytes = 4 * 1024 * 1024

// ReadLines calls visit for each complete line of r, newline stripped, and
// returns how many bytes it consumed. A last line without its newline is
// visited only when final says the input is finished; otherwise it is left
// unconsumed for the next read, because the writer may be midway through
// it. A line over MaxLineBytes is consumed without being visited. An error
// from visit stops the read before that line is counted.
func ReadLines(r io.Reader, final bool, visit func(line []byte) error) (int64, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var consumed int64
	for {
		line, n, err := nextLine(br)
		if err == io.EOF {
			if n == 0 || !final {
				return consumed, nil
			}
		} else if err != nil {
			return consumed, err
		}
		if line = trimEOL(line); len(line) > 0 {
			if verr := visit(line); verr != nil {
				return consumed, verr
			}
		}
		consumed += n
		if err == io.EOF {
			return consumed, nil
		}
	}
}

// nextLine returns the next line, newline included, and its length in
// bytes. A line longer than MaxLineBytes comes back as nil with its full
// length, so it is consumed without ever being held in memory.
func nextLine(br *bufio.Reader) ([]byte, int64, error) {
	var line []byte
	var n int64
	for {
		chunk, err := br.ReadSlice('\n')
		n += int64(len(chunk))
		if n <= MaxLineBytes+1 {
			line = append(line, chunk...)
		} else {
			line = nil
		}
		if err != bufio.ErrBufferFull {
			return line, n, err
		}
	}
}

func trimEOL(line []byte) []byte {
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line
}

// ReadFunc reads one file from its saved offset. r is positioned there and
// st is the state saved with it; ReadFunc advances st and returns the bytes
// it consumed — normally ReadLines with final=false.
type ReadFunc[S any] func(path string, r io.Reader, st *S) (int64, error)

// Tail tracks how far each file has been read. S is the reader state one
// line depends on from the lines before it. The zero value is ready to
// use. A Tail is not safe for concurrent use; a poller owns one and scans
// from a single goroutine.
type Tail[S any] struct {
	cursors map[string]*cursor[S]

	// FilesOpened and BytesParsed count work done, for tests to assert
	// that an unchanged file is not read again.
	FilesOpened int64
	BytesParsed int64
}

type cursor[S any] struct {
	size    int64
	modTime time.Time
	offset  int64 // bytes of complete lines consumed
	state   S
	id      os.FileInfo // which file this was; a replacement is another file
	head    []byte      // the first min(offset, headBytes) bytes as read
}

// Tracked reports how many files the Tail holds a cursor for.
func (t *Tail[S]) Tracked() int { return len(t.cursors) }

// Scan reads what each of paths gained since the last scan. Cursors for
// files not in paths are dropped, so deleted files do not accumulate.
// warn receives per-file read failures; the file is retried next scan.
func (t *Tail[S]) Scan(ctx context.Context, paths []string, read ReadFunc[S], warn func(path string, err error)) {
	live := make(map[string]*cursor[S], len(paths))
	for _, path := range paths {
		if cur := t.advance(path, t.cursors[path], read, warn); cur != nil {
			live[path] = cur
		}
		if ctx.Err() != nil {
			return
		}
	}
	t.cursors = live
}

// advance reads whatever path gained since cur and returns the cursor for
// the next scan. An unchanged file is not opened. A file that is no longer
// a continuation of what was read (smaller than the offset, a different
// file, or changed head bytes) was replaced, so it is read again from the
// start with fresh state; callers dedup what they emit, so a line they
// already saw does not go out twice.
func (t *Tail[S]) advance(path string, cur *cursor[S], read ReadFunc[S], warn func(string, error)) *cursor[S] {
	info, err := os.Stat(path)
	if err != nil {
		// Gone between the listing and the stat.
		return nil
	}
	if cur != nil && info.Size() == cur.size && info.ModTime().Equal(cur.modTime) {
		return cur
	}
	f, err := os.Open(path)
	if err != nil {
		warn(path, err)
		return cur
	}
	defer func() { _ = f.Close() }()
	next := cursor[S]{}
	if cur != nil && continues(f, info, cur) {
		next = *cur
	}
	next.size, next.modTime, next.id = info.Size(), info.ModTime(), info
	t.FilesOpened++
	if _, err := f.Seek(next.offset, io.SeekStart); err != nil {
		warn(path, err)
		return cur
	}
	n, err := read(path, f, &next.state)
	next.offset += n
	t.BytesParsed += n
	next.head = headOf(f, next.offset)
	if err != nil {
		warn(path, err)
		// Keep what was read, but look again next scan even if the file
		// has not changed, so a transient error does not park the rest.
		next.size = -1
	}
	return &next
}

// continues reports whether the open file is still the one cur read, past
// the bytes cur consumed: same file, not shorter than the offset, and the
// same first bytes.
func continues[S any](f *os.File, info os.FileInfo, cur *cursor[S]) bool {
	if info.Size() < cur.offset || (cur.offset > 0 && cur.head == nil) {
		return false
	}
	if cur.id != nil && !os.SameFile(cur.id, info) {
		return false
	}
	buf := make([]byte, len(cur.head))
	n, err := f.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		return false
	}
	return n == len(cur.head) && bytes.Equal(buf[:n], cur.head)
}

// headOf returns the first min(offset, headBytes) bytes of f, or nil when
// they cannot all be read.
func headOf(f *os.File, offset int64) []byte {
	want := min(offset, headBytes)
	buf := make([]byte, want)
	n, err := f.ReadAt(buf, 0)
	if int64(n) != want || (err != nil && err != io.EOF) {
		return nil
	}
	return buf
}
