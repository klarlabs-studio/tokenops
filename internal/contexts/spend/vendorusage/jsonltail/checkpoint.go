package jsonltail

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// A daemon start used to read every transcript again from byte zero: the
// cursors lived in memory only. On a machine with a few GB of Claude Code
// and Codex transcripts that held three background-priority cores for the
// first minute and more, and every other start-up task waited on them.
// A Tail's cursors are now saved (Checkpoint, Checkpointer) and a restart
// resumes from them (Restore), reading only what was appended since.

// Saved is one file's cursor in a checkpoint. It holds no file content:
// the file is recognised by a digest of its first bytes.
type Saved[S any] struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Offset  int64     `json:"offset"`
	HeadLen int64     `json:"head_len,omitempty"`
	HeadSum string    `json:"head_sum,omitempty"`
	State   S         `json:"state"`
}

// Checkpoint is the Tail's cursors, to Restore a new Tail from. A file
// whose last read failed is saved to be read again.
func (t *Tail[S]) Checkpoint() []Saved[S] {
	out := make([]Saved[S], 0, len(t.cursors))
	for path, c := range t.cursors {
		s := Saved[S]{Path: path, Size: c.size, ModTime: c.modTime, Offset: c.offset, State: c.state}
		if c.head.ok {
			s.HeadLen, s.HeadSum = c.head.n, hex.EncodeToString(c.head.sum[:])
		}
		out = append(out, s)
	}
	return out
}

// Restore sets the Tail's cursors to saved, as a checkpoint taken by
// Checkpoint holds them. A file that changed since is read on from its
// offset when its first bytes are still the ones read, and from the start
// otherwise; one with an unreadable digest is read from the start.
func (t *Tail[S]) Restore(saved []Saved[S]) {
	t.cursors = make(map[string]*cursor[S], len(saved))
	for _, s := range saved {
		c := &cursor[S]{size: s.Size, modTime: s.ModTime, offset: s.Offset, state: s.State}
		if sum, err := hex.DecodeString(s.HeadSum); err == nil && len(sum) == len(c.head.sum) && s.HeadLen > 0 {
			c.head = head{n: s.HeadLen, ok: true}
			copy(c.head.sum[:], sum)
		} else if s.Offset > 0 {
			// No way to tell it is the same file: read it again.
			c.offset, c.size = 0, -1
			var zero S
			c.state = zero
		}
		t.cursors[s.Path] = c
	}
}

// Checkpointer saves a value (a poller's cursors) to a file, lagging behind
// by Lag: Note is given the value after every scan, and writes the newest
// one that is at least Lag old. A scan's events are published to an
// asynchronous bus; saving cursors past events not yet stored would skip
// them for good if the daemon stopped first, while reading them again is
// harmless, since the store keeps one event per ID.
type Checkpointer[P any] struct {
	Path string
	// Version names the saved format; a file of another version is not
	// loaded, so the next start reads everything once.
	Version int
	Lag     time.Duration
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	pending []noted[P]
}

type noted[P any] struct {
	at time.Time
	v  P
}

type checkpointFile[P any] struct {
	Version int `json:"version"`
	Value   P   `json:"value"`
}

// Load is the saved value; false when there is none of this Version or it
// cannot be read.
func (c *Checkpointer[P]) Load() (P, bool) {
	var zero P
	b, err := os.ReadFile(c.Path)
	if err != nil {
		return zero, false
	}
	var f checkpointFile[P]
	if json.Unmarshal(b, &f) != nil || f.Version != c.Version {
		return zero, false
	}
	return f.Value, true
}

// Note records v as of now and writes the newest recorded value at least
// Lag old, if there is one not yet written. v must not change afterwards.
func (c *Checkpointer[P]) Note(v P) error {
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	c.pending = append(c.pending, noted[P]{at: now, v: v})
	due := -1
	for i, n := range c.pending {
		if now.Sub(n.at) >= c.Lag {
			due = i
		}
	}
	if due < 0 {
		return nil
	}
	write := c.pending[due].v
	c.pending = c.pending[due+1:]
	return c.write(write)
}

// write replaces the file with v, whole.
func (c *Checkpointer[P]) write(v P) error {
	b, err := json.Marshal(checkpointFile[P]{Version: c.Version, Value: v})
	if err != nil {
		return fmt.Errorf("jsonltail: checkpoint: %w", err)
	}
	dir := filepath.Dir(c.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("jsonltail: checkpoint: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".checkpoint-*")
	if err != nil {
		return fmt.Errorf("jsonltail: checkpoint: %w", err)
	}
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), c.Path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("jsonltail: checkpoint: %w", err)
	}
	return nil
}

// DefaultCheckpointLag is how far a poller's saved positions lag its reads:
// longer than the event bus takes to store what a scan published.
const DefaultCheckpointLag = 2 * time.Minute

// Lag is a poller option's checkpoint lag: zero is DefaultCheckpointLag,
// a negative one none.
func Lag(opt time.Duration) time.Duration {
	switch {
	case opt == 0:
		return DefaultCheckpointLag
	case opt < 0:
		return 0
	}
	return opt
}
