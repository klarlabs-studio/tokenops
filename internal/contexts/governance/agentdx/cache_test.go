package agentdx

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var cacheEpoch = time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)

// claudeTurn is one instruction and the agent's answer to it, at at.
func claudeTurn(at time.Time, i int) string {
	ts := func(d time.Duration) string { return at.Add(d).Format(time.RFC3339Nano) }
	return strings.Join([]string{
		fmt.Sprintf(`{"type":"user","timestamp":%q,"sessionId":"s%d","message":{"content":"task %d"}}`, ts(0), i%3, i),
		fmt.Sprintf(`{"type":"assistant","timestamp":%q,"sessionId":"s%d","effort":"high","message":{"model":"claude-opus-5-5","usage":{"input_tokens":%d,"cache_read_input_tokens":900},"content":[{"type":"text","text":"ok"},{"type":"tool_use","name":"Read","input":{"file_path":"/r/f%d.go"}}]}}`, ts(time.Second), i%3, 100+i, i%5),
		fmt.Sprintf(`{"type":"user","timestamp":%q,"sessionId":"s%d","message":{"content":[{"type":"tool_result"}]}}`, ts(2*time.Second), i%3),
		`not json at all`,
	}, "\n") + "\n"
}

// codexTurn is one Codex turn: its context line, the instruction, the
// context size and the answer.
func codexTurn(at time.Time, i int) string {
	ts := func(d time.Duration) string { return at.Add(d).Format(time.RFC3339Nano) }
	return strings.Join([]string{
		fmt.Sprintf(`{"timestamp":%q,"type":"turn_context","payload":{"model":"gpt-5.5-%d","effort":"High"}}`, ts(0), i%2),
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"user_message","message":"do %d"}}`, ts(time.Minute), i),
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":%d}}}}`, ts(2*time.Minute), 500+i),
		fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`, ts(3*time.Minute)),
	}, "\n") + "\n"
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// A later mtime than the previous write, whatever the clock's grain.
	bump(t, path, time.Second)
}

func bump(t *testing.T, path string, d time.Duration) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	m := info.ModTime().Add(d)
	if err := os.Chtimes(path, m, m); err != nil {
		t.Fatal(err)
	}
}

// turns writes n turns from start, an hour apart.
func turns(start time.Time, n, from int, turn func(time.Time, int) string) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(turn(start.Add(time.Duration(i)*time.Hour), from+i))
	}
	return b.String()
}

// sameRecords fails unless the cached scan yields exactly the full one.
func sameRecords(t *testing.T, step string, cached, full []Record) {
	t.Helper()
	if len(cached) != len(full) {
		t.Fatalf("%s: cached scan yields %d records, a full read %d", step, len(cached), len(full))
	}
	if len(full) > 0 && !reflect.DeepEqual(cached, full) {
		for i := range full {
			if !reflect.DeepEqual(cached[i], full[i]) {
				t.Fatalf("%s: record %d differs:\ncached %+v\nfull   %+v", step, i, cached[i], full[i])
			}
		}
	}
}

func scanBoth(t *testing.T, step string, cache *TranscriptCache, ccRoot, cxRoot string, since time.Time) {
	t.Helper()
	full, err := Extract(ExtractOptions{Root: ccRoot, Since: since, IncludeScratch: true})
	if err != nil {
		t.Fatal(err)
	}
	cached, err := Extract(ExtractOptions{Root: ccRoot, Since: since, IncludeScratch: true, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	sameRecords(t, step+" (claude code)", cached, full)

	full, err = ExtractCodex(ExtractOptions{Root: cxRoot, Since: since})
	if err != nil {
		t.Fatal(err)
	}
	cached, err = ExtractCodex(ExtractOptions{Root: cxRoot, Since: since, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	sameRecords(t, step+" (codex)", cached, full)
}

// A cached scan yields exactly what reading every file again yields,
// across files that grow (including a line still being written), files
// that are deleted or added, a cut-off that moves forward past a Codex
// turn's context line, and a file last written before the window.
func TestTranscriptCacheMatchesFullRead(t *testing.T) {
	dir := t.TempDir()
	cc := filepath.Join(dir, "claude")
	cx := filepath.Join(dir, "codex")

	growing := filepath.Join(cc, "proj-a", "grow.jsonl")
	writeFile(t, growing, turns(cacheEpoch, 30, 0, claudeTurn)+`{"type":"user","timestamp":"2026-08-21T`) // half-written
	steady := filepath.Join(cc, "proj-a", "steady.jsonl")
	writeFile(t, steady, turns(cacheEpoch.Add(-48*time.Hour), 60, 100, claudeTurn))
	doomed := filepath.Join(cc, "proj-b", "doomed.jsonl")
	writeFile(t, doomed, turns(cacheEpoch, 10, 200, claudeTurn))
	old := filepath.Join(cc, "proj-b", "old.jsonl")
	writeFile(t, old, turns(cacheEpoch.Add(-30*24*time.Hour), 5, 300, claudeTurn))
	oldTime := cacheEpoch.Add(-29 * 24 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	rollout := filepath.Join(cx, "2026", "08", "20", "rollout-2026-08-20T00-00-00-aaa.jsonl")
	writeFile(t, rollout, turns(cacheEpoch.Add(-3*time.Hour), 12, 0, codexTurn))
	straddle := filepath.Join(cx, "2026", "08", "19", "rollout-2026-08-19T00-00-00-bbb.jsonl")
	writeFile(t, straddle, turns(cacheEpoch.Add(-10*time.Hour), 20, 50, codexTurn))

	cache := NewTranscriptCache()
	since := cacheEpoch.Add(-time.Hour)
	scanBoth(t, "first scan", cache, cc, cx, since)
	if cache.Len() != 5 {
		t.Errorf("cache holds %d files, want 5 (the file written before the window is not read)", cache.Len())
	}

	// Nothing changed: the cache answers alone.
	scanBoth(t, "unchanged", cache, cc, cx, since)

	// The half-written line is finished and more is written; one file is
	// deleted, another added; a rollout grows; the cut-off moves past
	// the context lines of a straddling rollout's turns.
	appendFile(t, growing, `00:00:00Z","sessionId":"s9","message":{"content":"finish it"}}`+"\n"+turns(cacheEpoch.Add(40*time.Hour), 5, 400, claudeTurn))
	if err := os.Remove(doomed); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cc, "proj-c", "new.jsonl"), turns(cacheEpoch.Add(5*time.Hour), 8, 500, claudeTurn))
	appendFile(t, rollout, turns(cacheEpoch.Add(20*time.Hour), 3, 60, codexTurn))
	since = cacheEpoch.Add(90 * time.Second)
	scanBoth(t, "after changes", cache, cc, cx, since)
	if cache.Len() != 5 {
		t.Errorf("cache holds %d files, want 5 (the deleted file forgotten)", cache.Len())
	}

	// A line still being written at the end, then finished.
	appendFile(t, steady, `{"type":"assistant","timestamp":"2026-08-23T00:00:00Z","sessionId":"s1","message":{"model":"m","content":[]`)
	scanBoth(t, "partial line", cache, cc, cx, since)
	appendFile(t, steady, "}}\n")
	scanBoth(t, "partial line finished", cache, cc, cx, since)

	// A file rewritten shorter is read again from the start.
	writeFile(t, growing, turns(cacheEpoch.Add(50*time.Hour), 2, 600, claudeTurn))
	bump(t, growing, time.Hour)
	scanBoth(t, "rewritten", cache, cc, cx, since)

	// An earlier cut-off than the cache was read with reads again.
	scanBoth(t, "earlier cut-off", cache, cc, cx, cacheEpoch.Add(-24*time.Hour))
	scanBoth(t, "no cut-off", cache, cc, cx, time.Time{})
}

// A scan that carries prompt text never uses the cache, so the words do
// not outlive it.
func TestTranscriptCacheHoldsNoPromptText(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "p", "s.jsonl"), turns(cacheEpoch, 3, 0, claudeTurn))
	cache := NewTranscriptCache()
	got, err := Extract(ExtractOptions{Root: dir, WithPromptText: true, IncludeScratch: true, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || cache.Len() != 0 {
		t.Fatalf("records %d, cached files %d; want records and an empty cache", len(got), cache.Len())
	}
}

// A file last written before the cut-off cannot hold an entry after it,
// so it is not opened.
func TestExtractSkipsFilesWrittenBeforeTheWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p", "s.jsonl")
	// A timestamp after the cut-off in a file stamped before it can only
	// come from a clock gone wrong; the file's time decides.
	writeFile(t, path, turns(cacheEpoch, 2, 0, claudeTurn))
	stamp := cacheEpoch.Add(-48 * time.Hour)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	got, err := Extract(ExtractOptions{Root: dir, Since: cacheEpoch.Add(-time.Hour), IncludeScratch: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("read %d records from a file last written before the window", len(got))
	}
}
