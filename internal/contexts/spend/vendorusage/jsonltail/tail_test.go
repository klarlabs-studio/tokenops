package jsonltail

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lineState numbers lines across reads, so a test can see whether state
// survived the gap between two scans.
type lineState struct{ n int }

type seenLine struct {
	n    int
	text string
}

type fixture struct {
	t    *testing.T
	path string
	tail Tail[lineState]
	seen []seenLine
	errs []error
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{t: t, path: filepath.Join(t.TempDir(), "sess.jsonl")}
}

func (f *fixture) write(s string) {
	f.t.Helper()
	if err := os.WriteFile(f.path, []byte(s), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) appendText(s string) {
	f.t.Helper()
	fh, err := os.OpenFile(f.path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = fh.Close() }()
	if _, err := fh.WriteString(s); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) scan(paths ...string) {
	if len(paths) == 0 {
		paths = []string{f.path}
	}
	f.tail.Scan(context.Background(), paths, func(_ string, r io.Reader, st *lineState) (int64, error) {
		return ReadLines(r, false, func(line []byte) error {
			st.n++
			f.seen = append(f.seen, seenLine{n: st.n, text: string(line)})
			return nil
		})
	}, func(_ string, err error) { f.errs = append(f.errs, err) })
}

func (f *fixture) texts() []string {
	out := make([]string, len(f.seen))
	for i, s := range f.seen {
		out[i] = s.text
	}
	return out
}

func TestScanParsesOnlyAppendedBytes(t *testing.T) {
	f := newFixture(t)
	f.write("a\nb\n")

	f.scan()
	if f.tail.BytesParsed != 4 {
		t.Fatalf("first scan parsed %d bytes, want 4 (the whole file)", f.tail.BytesParsed)
	}

	f.scan()
	if f.tail.BytesParsed != 4 || f.tail.FilesOpened != 1 {
		t.Fatalf("an unchanged file was read again: %d bytes parsed, %d opens; want 4 and 1",
			f.tail.BytesParsed, f.tail.FilesOpened)
	}

	f.appendText("c\n")
	f.scan()
	if f.tail.BytesParsed != 6 {
		t.Fatalf("after appending 2 bytes parsed %d total, want 6", f.tail.BytesParsed)
	}
	if got := strings.Join(f.texts(), ","); got != "a,b,c" {
		t.Errorf("lines = %s, want a,b,c — each exactly once", got)
	}
}

func TestScanCarriesStateAcrossReads(t *testing.T) {
	f := newFixture(t)
	f.write("a\nb\n")
	f.scan()
	f.appendText("c\n")
	f.scan()
	if last := f.seen[len(f.seen)-1]; last.n != 3 {
		t.Errorf("the appended line was numbered %d, want 3 — state from the first read was lost", last.n)
	}
}

// The writer may be midway through a line when a scan lands. That line is
// left for the next scan rather than read as a fragment and stepped over.
func TestScanLeavesAnUnterminatedLineForLater(t *testing.T) {
	f := newFixture(t)
	f.write("first\nsec")
	f.scan()
	if got := strings.Join(f.texts(), ","); got != "first" {
		t.Fatalf("lines = %s, want only the terminated one", got)
	}
	f.appendText("ond\n")
	f.scan()
	if got := strings.Join(f.texts(), ","); got != "first,second" {
		t.Errorf("lines = %s, want first,second — the finished line read whole", got)
	}
}

// A file smaller than what was already read was replaced. The saved offset
// points past its end, so it is read again from the start with fresh state.
func TestScanRereadsAFileThatShrank(t *testing.T) {
	f := newFixture(t)
	f.write("aaaa\nbbbb\n")
	f.scan()
	f.write("z\n")
	f.scan()
	last := f.seen[len(f.seen)-1]
	if last.text != "z" || last.n != 1 {
		t.Errorf("after replacement read %q as line %d, want \"z\" as line 1", last.text, last.n)
	}
}

func TestScanForgetsFilesThatAreGone(t *testing.T) {
	f := newFixture(t)
	f.write("a\n")
	f.scan()
	if f.tail.Tracked() != 1 {
		t.Fatalf("tracking %d files, want 1", f.tail.Tracked())
	}
	// The next listing no longer includes the transcript.
	f.scan(filepath.Join(t.TempDir(), "other.jsonl"))
	if f.tail.Tracked() != 0 {
		t.Errorf("still tracking %d files after the transcript stopped being listed", f.tail.Tracked())
	}
}

// Before, one oversized line stopped every later line in that file from
// being read. Now it is consumed unread and the file continues.
func TestReadLinesSkipsAnOversizedLineAndContinues(t *testing.T) {
	f := newFixture(t)
	f.write(strings.Repeat("x", MaxLineBytes+1) + "\nafter\n")
	f.scan()
	if got := strings.Join(f.texts(), ","); got != "after" {
		t.Errorf("lines = %.40s, want only \"after\"", got)
	}
	if len(f.errs) != 0 {
		t.Errorf("an oversized line was reported as a read failure: %v", f.errs)
	}
}

// Read in one pass with final=true, an input's last line counts even
// without its newline: a finished file is not waiting on a writer.
func TestReadLinesFinalReadsTheLastLine(t *testing.T) {
	var got []string
	n, err := ReadLines(strings.NewReader("a\nb"), true, func(line []byte) error {
		got = append(got, string(line))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "a,b" || n != 3 {
		t.Errorf("got %v consuming %d bytes, want [a b] consuming 3", got, n)
	}
}
