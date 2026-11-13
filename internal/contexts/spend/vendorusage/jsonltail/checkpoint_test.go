package jsonltail

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A Tail restored from a checkpoint, as a restarted daemon's is, reads
// only what was appended since, with the reader state carried over.
func TestRestoredTailReadsOnlyWhatWasAppended(t *testing.T) {
	f := newFixture(t)
	f.write("a\nb\n")
	f.scan()
	saved := f.tail.Checkpoint()
	f.appendText("c\n")

	restarted := newFixture(t)
	restarted.path = f.path
	restarted.tail.Restore(saved)
	restarted.scan()
	if got := restarted.texts(); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("after a restore read %q, want only the appended line", got)
	}
	if restarted.seen[0].n != 3 {
		t.Fatalf("line numbered %d, want 3: the reader state was not restored", restarted.seen[0].n)
	}
	if restarted.tail.BytesParsed != 2 {
		t.Fatalf("parsed %d bytes, want 2", restarted.tail.BytesParsed)
	}
}

// A file replaced while the daemon was down (other first lines, same or
// larger size) is read again from the start, as a live Tail would.
func TestRestoredTailRereadsAReplacedFile(t *testing.T) {
	f := newFixture(t)
	f.write("first\nsecond\n")
	f.scan()
	saved := f.tail.Checkpoint()
	f.write("FIRST\nsecond\nthird\n")

	restarted := newFixture(t)
	restarted.path = f.path
	restarted.tail.Restore(saved)
	restarted.scan()
	if got := restarted.texts(); !slices.Equal(got, []string{"FIRST", "second", "third"}) {
		t.Fatalf("a replaced file read %q, want it read whole", got)
	}
}

// A checkpoint holds no transcript text: a file is recognised by a digest
// of its first bytes, never the bytes, which can be a prompt.
func TestCheckpointHoldsNoFileContent(t *testing.T) {
	f := newFixture(t)
	f.write(`{"type":"user","message":"my secret prompt"}` + "\n")
	f.scan()
	b, err := json.Marshal(f.tail.Checkpoint())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret") {
		t.Fatalf("the checkpoint holds the file's text: %s", b)
	}
}

// A Checkpointer writes, behind the reads by its lag, the newest value it
// was given that is at least that old: what was read in the last lag may
// not be stored yet, so a restart reads it again rather than skip it.
func TestCheckpointerWritesBehindTheReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "tail.json")
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	c := &Checkpointer[int]{Path: path, Version: 1, Lag: 2 * time.Minute, Now: func() time.Time { return now }}

	step := func(v int, d time.Duration) {
		t.Helper()
		now = now.Add(d)
		if err := c.Note(v); err != nil {
			t.Fatal(err)
		}
	}
	loaded := func() (int, bool) {
		return (&Checkpointer[int]{Path: path, Version: 1}).Load()
	}
	step(1, 0)
	step(2, time.Minute)
	if _, ok := loaded(); ok {
		t.Fatal("wrote a value younger than the lag")
	}
	step(3, 90*time.Second) // 1 is 150s old, 2 is 90s
	if v, ok := loaded(); !ok || v != 1 {
		t.Fatalf("loaded %d, %v; want 1, the newest at least the lag old", v, ok)
	}
	step(4, 2*time.Minute) // 2 is 210s old, 3 is 120s
	if v, _ := loaded(); v != 3 {
		t.Fatalf("loaded %d, want 3", v)
	}
	if n := len(c.pending); n > 2 {
		t.Fatalf("%d values pending, want the written ones dropped", n)
	}

	if _, ok := (&Checkpointer[int]{Path: path, Version: 2}).Load(); ok {
		t.Fatal("loaded a checkpoint of another version")
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded(); ok {
		t.Fatal("loaded an unreadable checkpoint")
	}
}

// A checkpoint's temp file left by a writer killed before its rename is
// cleared by the next write, instead of staying beside the checkpoint.
func TestCheckpointWriteClearsALeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, ".checkpoint-123456")
	if err := os.WriteFile(leftover, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(leftover, old, old); err != nil {
		t.Fatal(err)
	}
	c := &Checkpointer[int]{Path: filepath.Join(dir, "checkpoint.json"), Version: 1}
	if err := c.write(1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("leftover temp file still there (stat err %v)", err)
	}
}
