package agentdx

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

// A cache saved and loaded by a restarted daemon yields exactly what
// reading the files yields, without reading them again; a file that grew
// meanwhile is read on from where the save stopped, and a line still
// being written when it was saved is read again, not taken from the save.
func TestSavedCacheResumesWithoutReading(t *testing.T) {
	dir := t.TempDir()
	cc, cx := filepath.Join(dir, "claude"), filepath.Join(dir, "codex")
	ccFile := filepath.Join(cc, "p", "s.jsonl")
	cxFile := filepath.Join(cx, "2026", "08", "20", "rollout-2026-08-20T00-00-00-aaa.jsonl")
	writeFile(t, ccFile, turns(cacheEpoch, 4, 0, claudeTurn))
	// A Codex instruction still being written: its words are on the
	// unterminated last line.
	writeFile(t, cxFile, turns(cacheEpoch, 4, 0, codexTurn)+`{"timestamp":"2026-08-20T09:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"secret words"}}`)
	first := NewTranscriptCache()
	scanBoth(t, "first", first, cc, cx, cacheEpoch)

	saved := filepath.Join(dir, "state", "transcripts.gob")
	if err := first.Save(saved, "v1+abc"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(saved); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("saved file %v, %v; want owner-only", info, err)
	}
	if b, _ := os.ReadFile(saved); bytes.Contains(b, []byte("secret words")) {
		t.Fatal("the save holds an instruction's words")
	}

	second := NewTranscriptCache()
	if err := second.Load(saved, "v1+abc"); err != nil {
		t.Fatal(err)
	}
	if second.Len() != 2 {
		t.Fatalf("loaded %d files; want 2", second.Len())
	}
	scanBoth(t, "after a load", second, cc, cx, cacheEpoch)
	ccBytes, cxBytes := second.bytesRead()
	if ccBytes != 0 {
		t.Errorf("read %d Claude Code bytes after a load; want none", ccBytes)
	}
	if line := int64(len(`{"timestamp":"2026-08-20T09:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"secret words"}}`)); cxBytes != line {
		t.Errorf("read %d Codex bytes after a load; want only the unfinished line's %d", cxBytes, line)
	}

	appendFile(t, ccFile, turns(cacheEpoch.Add(5*60*60*1e9), 2, 4, claudeTurn))
	scanBoth(t, "grown after a load", second, cc, cx, cacheEpoch)
}

func TestSavedCacheFromAnotherBuildIsNotLoaded(t *testing.T) {
	dir := t.TempDir()
	cc := filepath.Join(dir, "claude")
	writeFile(t, filepath.Join(cc, "p", "s.jsonl"), turns(cacheEpoch, 2, 0, claudeTurn))
	facts := func(string) PromptFacts { return PromptFacts{Known: true} }
	c := NewTranscriptCacheWithFacts(facts)
	if _, err := Extract(ExtractOptions{Root: cc, IncludeScratch: true, Cache: c}); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(dir, "transcripts.gob")
	if err := c.Save(saved, "v1+abc"); err != nil {
		t.Fatal(err)
	}
	for name, load := range map[string]func() error{
		"another build":         func() error { return NewTranscriptCacheWithFacts(facts).Load(saved, "v2+def") },
		"a cache without facts": func() error { return NewTranscriptCache().Load(saved, "v1+abc") },
	} {
		if err := load(); !errors.Is(err, ErrSavedCacheStale) {
			t.Errorf("%s: err = %v; want ErrSavedCacheStale", name, err)
		}
	}
	if err := NewTranscriptCacheWithFacts(facts).Load(filepath.Join(dir, "none.gob"), "v1+abc"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing save: err = %v", err)
	}
}

// A loaded cache shares the strings its records repeat, as a cache read
// from the transcripts does, instead of holding a copy per record.
func TestLoadedCacheSharesRepeatedStrings(t *testing.T) {
	dir := t.TempDir()
	cc, cx := filepath.Join(dir, "claude"), filepath.Join(dir, "codex")
	writeFile(t, filepath.Join(cc, "p", "s.jsonl"), turns(cacheEpoch, 4, 0, claudeTurn))
	first := NewTranscriptCache()
	scanBoth(t, "first", first, cc, cx, cacheEpoch)
	saved := filepath.Join(dir, "transcripts.gob")
	if err := first.Save(saved, "v1"); err != nil {
		t.Fatal(err)
	}
	second := NewTranscriptCache()
	if err := second.Load(saved, "v1"); err != nil {
		t.Fatal(err)
	}
	var recs []Record
	for _, e := range second.claude {
		recs = append(recs, e.records...)
	}
	if len(recs) < 2 {
		t.Fatalf("loaded %d records; want several", len(recs))
	}
	held := map[string]*byte{}
	for _, r := range recs {
		p := unsafe.StringData(r.SessionID)
		if first, ok := held[r.SessionID]; ok && first != p {
			t.Fatalf("records hold their own copy of session %q", r.SessionID)
		}
		held[r.SessionID] = p
	}
	if len(held) >= len(recs) {
		t.Fatalf("%d sessions in %d records; the fixture repeats none", len(held), len(recs))
	}
	scanBoth(t, "after a load", second, cc, cx, cacheEpoch)
}
