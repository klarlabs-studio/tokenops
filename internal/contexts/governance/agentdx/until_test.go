package agentdx

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// weekFiles writes, for each of weeks, one Claude Code transcript and one
// Codex rollout holding a day of turns from that week's Wednesday, each
// last written when its last turn was, and returns their paths.
func weekFiles(t *testing.T, cc, cx string, weeks []time.Time) (claude, codex []string) {
	t.Helper()
	for i, week := range weeks {
		w := week.AddDate(0, 0, 2)
		last := w.Add(23*time.Hour + 3*time.Minute)
		c := filepath.Join(cc, "p", fmt.Sprintf("w%d.jsonl", i))
		writeFile(t, c, turns(w, 24, 100*i, claudeTurn))
		x := filepath.Join(cx, w.Format("2006/01/02"), "rollout-"+w.Format("2006-01-02T15-04-05")+fmt.Sprintf("-w%d.jsonl", i))
		writeFile(t, x, turns(w, 24, 100*i, codexTurn))
		for _, p := range []string{c, x} {
			if err := os.Chtimes(p, last, last); err != nil {
				t.Fatal(err)
			}
		}
		claude, codex = append(claude, c), append(codex, x)
	}
	return claude, codex
}

func sizeOf(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

// A scan bounded on both sides yields only the window's records and reads
// only the transcripts that can hold them: a week's figures, computed
// weeks after it ended, read that week's files, not every file written
// since (the daemon read 16 weeks of transcripts to compute the oldest).
func TestScanUntilReadsOnlyTheWindowsFiles(t *testing.T) {
	dir := t.TempDir()
	cc, cx := filepath.Join(dir, "claude"), filepath.Join(dir, "codex")
	weeks := []time.Time{cacheEpoch.AddDate(0, 0, -21), cacheEpoch.AddDate(0, 0, -14), cacheEpoch.AddDate(0, 0, -7)}
	claude, codex := weekFiles(t, cc, cx, weeks)
	since, until := weeks[1], weeks[1].AddDate(0, 0, 7)

	cache := NewTranscriptCache()
	ccRecs, err := Extract(ExtractOptions{Root: cc, Since: since, Until: until, IncludeScratch: true, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	cxRecs, err := ExtractCodex(ExtractOptions{Root: cx, Since: since, Until: until, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	for _, recs := range [][]Record{ccRecs, cxRecs} {
		if len(recs) == 0 {
			t.Fatal("no records in the window")
		}
		for _, r := range recs {
			if r.At.Before(since) || !r.At.Before(until) {
				t.Fatalf("record at %s outside [%s, %s)", r.At, since, until)
			}
		}
	}
	gotCC, gotCX := cache.bytesRead()
	if want := sizeOf(t, claude[1]); gotCC != want {
		t.Errorf("read %d Claude Code bytes, want the week's one file (%d)", gotCC, want)
	}
	if want := sizeOf(t, codex[1]); gotCX != want {
		t.Errorf("read %d Codex bytes, want the week's one rollout (%d)", gotCX, want)
	}

	// Without a cache the window is the same.
	full, err := Extract(ExtractOptions{Root: cc, Since: since, Until: until, IncludeScratch: true})
	if err != nil {
		t.Fatal(err)
	}
	sameRecords(t, "claude code without a cache", full, ccRecs)
	full, err = ExtractCodex(ExtractOptions{Root: cx, Since: since, Until: until})
	if err != nil {
		t.Fatal(err)
	}
	sameRecords(t, "codex without a cache", full, cxRecs)
}

// heldBefore counts the records the cache holds from before t.
func heldBefore(c *TranscriptCache, t time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.claude {
		for _, r := range append(append([]Record(nil), e.records...), e.tail...) {
			if r.At.Before(t) {
				n++
			}
		}
	}
	for _, e := range c.codexs {
		for _, r := range e.reader.records() {
			if r.At.Before(t) {
				n++
			}
		}
	}
	return n
}

// A scan reaching further back than a shared cache keeps reads the files
// without the cache: the cache goes on holding its own window, not the
// scan's. The daemon's oldest weekly figures used to pull 16 weeks of every
// transcript into the cache every reader shares.
func TestAScanOlderThanTheCacheLeavesItAsItWas(t *testing.T) {
	dir := t.TempDir()
	cc, cx := filepath.Join(dir, "claude"), filepath.Join(dir, "codex")
	now := cacheEpoch
	writeFile(t, filepath.Join(cc, "p", "long.jsonl"), turns(now.AddDate(0, 0, -30), 24*30, 0, claudeTurn))
	writeFile(t, filepath.Join(cx, "2026", "07", "21", "rollout-2026-07-21T00-00-00-long.jsonl"), turns(now.AddDate(0, 0, -30), 24*30, 0, codexTurn))
	keep := now.AddDate(0, 0, -15)
	cache := NewTranscriptCache(KeepFrom(func() time.Time { return keep }))
	scanBoth(t, "14 days", cache, cc, cx, now.AddDate(0, 0, -14))
	held := cache.Len()
	readCC, readCX := cache.bytesRead()

	since, until := now.AddDate(0, 0, -30), now.AddDate(0, 0, -23)
	for _, s := range []struct {
		root string
		scan func(ExtractOptions) ([]Record, error)
	}{{cc, Extract}, {cx, ExtractCodex}} {
		recs, err := s.scan(ExtractOptions{Root: s.root, Since: since, Until: until, IncludeScratch: true, Cache: cache})
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) == 0 {
			t.Fatalf("%s: no records in the older window", s.root)
		}
	}
	if n := heldBefore(cache, keep); n != 0 {
		t.Errorf("the cache holds %d records from before its floor after an older scan", n)
	}
	if cache.Len() != held {
		t.Errorf("the cache holds %d files after an older scan, want %d", cache.Len(), held)
	}
	if cc, cx := cache.bytesRead(); cc != readCC || cx != readCX {
		t.Errorf("the older scan read through the cache (%d+%d bytes)", cc-readCC, cx-readCX)
	}
	// The cache still serves its own window without reading anything.
	scanBoth(t, "14 days again", cache, cc, cx, now.AddDate(0, 0, -14))
	if cc, cx := cache.bytesRead(); cc != readCC || cx != readCX {
		t.Errorf("the cache read %d+%d bytes again for its own window", cc-readCC, cx-readCX)
	}
}

// A transcript is skipped only when it is known to begin a day or more
// after the window ends; one whose start cannot be read is read.
func TestBegunAfter(t *testing.T) {
	dir := t.TempDir()
	until := cacheEpoch
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		writeFile(t, p, body)
		return p
	}
	stamped := func(at time.Time) string {
		return fmt.Sprintf(`{"type":"summary","summary":"x"}`+"\n"+`{"type":"user","timestamp":%q}`+"\n", at.Format(time.RFC3339Nano))
	}
	for _, tc := range []struct {
		name  string
		path  string
		codex bool
		until time.Time
		want  bool
	}{
		{"no until", write("a.jsonl", stamped(until.AddDate(0, 0, 3))), false, time.Time{}, false},
		{"begun two days after", write("b.jsonl", stamped(until.AddDate(0, 0, 2))), false, until, true},
		{"begun an hour after", write("c.jsonl", stamped(until.Add(time.Hour))), false, until, false},
		{"begun before", write("d.jsonl", stamped(until.AddDate(0, 0, -1))), false, until, false},
		{"no stamped line", write("e.jsonl", `{"type":"summary"}`+"\n"), false, until, false},
		{"missing", filepath.Join(dir, "gone.jsonl"), false, until, false},
		{"rollout begun after", filepath.Join(dir, "rollout-2026-08-22T10-00-00-x.jsonl"), true, until, true},
		{"rollout begun the next morning", filepath.Join(dir, "rollout-2026-08-20T10-00-00-x.jsonl.zst"), true, until, false},
		{"rollout without a start", filepath.Join(dir, "rollout-x.jsonl"), true, until, false},
	} {
		if got := begunAfter(tc.path, tc.until, tc.codex); got != tc.want {
			t.Errorf("%s: begunAfter = %v, want %v", tc.name, got, tc.want)
		}
	}
}
