package agentdx

import (
	"bufio"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// A saved cache lets a restarted daemon pick up where the last one
// stopped: without one, its first scans read every transcript of the
// window again, minutes of several cores on a busy machine, after every
// upgrade and every login.
//
// What is saved is what the cache holds, which is never an instruction's
// words: the cache reads without them, and a tool call's arguments are
// kept only as a digest (callSignature). The file is written with
// owner-only permissions. A saved cache is used only by the build that
// saved it (key), since another may read the transcripts differently, and
// only into a cache that derives the same prompt facts. Each entry is
// checked against its file as any cached entry is: a file that changed is
// read on, or again.

// savedFormat is the layout of a saved cache.
const savedFormat = 1

type savedCache struct {
	Format int
	// Key names the build that saved it.
	Key string
	// Facts is whether the cache derived prompt facts.
	Facts  bool
	Claude map[string]savedClaude
	Codex  map[string]savedCodex
}

type savedClaude struct {
	Size    int64
	MTime   time.Time
	Since   time.Time
	Records []Record
	Offset  int64
	Stopped bool
}

type savedCodex struct {
	Size    int64
	MTime   time.Time
	Offset  int64
	Stopped bool
	Reader  savedCodexReader
}

type savedCodexReader struct {
	SessionID                 string
	Since                     time.Time
	Out                       []Record
	EventPrompts, ItemPrompts []Record
	ItemSeen                  bool
	PendingContext            int64
	Model, Effort, Role       string
}

// Save writes what the cache holds to path, replacing the file whole,
// under key, which names the build saving it.
func (c *TranscriptCache) Save(path, key string) error {
	s := c.snapshot(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".transcripts-*")
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(tmp, 1<<20)
	err = gob.NewEncoder(w).Encode(s)
	if err == nil {
		err = w.Flush()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// snapshot copies what the cache holds. An entry whose last line is still
// being written is saved as though the file ended before that line, so
// the line, which may be a Codex prompt's raw words, is not saved; the
// first scan after a load reads it again. A compressed rollout is left
// out: it is read whole when it changes anyway.
func (c *TranscriptCache) snapshot(key string) savedCache {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := savedCache{Format: savedFormat, Key: key, Facts: c.facts != nil,
		Claude: make(map[string]savedClaude, len(c.claude)), Codex: make(map[string]savedCodex, len(c.codexs))}
	for p, e := range c.claude {
		size := e.stamp.size
		if len(e.tail) > 0 {
			size = e.offset
		}
		s.Claude[p] = savedClaude{Size: size, MTime: e.stamp.mtime, Since: e.since,
			Records: withoutText(e.records), Offset: e.offset, Stopped: e.stopped}
	}
	for p, e := range c.codexs {
		if e.compressed {
			continue
		}
		size := e.stamp.size
		if len(e.tail) > 0 {
			size = e.offset
		}
		r := e.reader
		s.Codex[p] = savedCodex{Size: size, MTime: e.stamp.mtime, Offset: e.offset, Stopped: e.stopped,
			Reader: savedCodexReader{SessionID: r.sessionID, Since: r.since, Out: withoutText(r.out),
				EventPrompts: withoutText(r.eventPrompts), ItemPrompts: withoutText(r.itemPrompts), ItemSeen: r.itemSeen,
				PendingContext: r.pendingContext, Model: r.model, Effort: r.effort, Role: r.role}}
	}
	return s
}

// withoutText is recs with no instruction's words: the cache never holds
// them, and this keeps a save from writing any should that change.
func withoutText(recs []Record) []Record {
	for _, r := range recs {
		if r.Text != "" {
			out := make([]Record, len(recs))
			copy(out, recs)
			for i := range out {
				out[i].Text = ""
			}
			return out
		}
	}
	return recs
}

// ErrSavedCacheStale is a saved cache another build wrote, or one that
// derived other prompt facts: it is not loaded.
var ErrSavedCacheStale = errors.New("saved transcript cache is from another build")

// Load fills an empty cache from what Save wrote at path under key. A
// missing file is fs.ErrNotExist; a cache that already holds entries is
// left as it is.
func (c *TranscriptCache) Load(path, key string) error {
	f, err := os.Open(path) //nolint:gosec // the daemon's own state file
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var s savedCache
	if err := gob.NewDecoder(bufio.NewReaderSize(f, 1<<20)).Decode(&s); err != nil {
		return fmt.Errorf("read saved transcript cache: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.Format != savedFormat || s.Key != key || s.Facts != (c.facts != nil) {
		return ErrSavedCacheStale
	}
	if len(c.claude) > 0 || len(c.codexs) > 0 {
		return nil
	}
	for p, e := range s.Claude {
		c.claude[p] = &claudeEntry{stamp: fileStamp{size: e.Size, mtime: e.MTime}, since: e.Since,
			records: e.Records, offset: e.Offset, stopped: e.Stopped}
	}
	for p, e := range s.Codex {
		r := e.Reader
		c.codexs[p] = &codexEntry{stamp: fileStamp{size: e.Size, mtime: e.MTime}, offset: e.Offset, stopped: e.Stopped,
			reader: &codexReader{sessionID: r.SessionID, since: r.Since, facts: c.facts, out: r.Out,
				eventPrompts: r.EventPrompts, itemPrompts: r.ItemPrompts, itemSeen: r.ItemSeen,
				pendingContext: r.PendingContext, model: r.Model, effort: r.Effort, role: r.Role}}
	}
	return nil
}
