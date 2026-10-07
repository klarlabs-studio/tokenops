// Package followthrough stores the coach's follow-through ledger as JSON
// lines under ~/.tokenops/coach. Hooks append to it from many sessions at
// once, so every write takes an exclusive lock on the file.
package followthrough

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/filelock"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

// pruneAbove is the size past which an append also drops entries older
// than keep. A line is ~200 bytes, so this is several thousand
// interventions: months of normal use.
const pruneAbove = 1 << 20

// keep is how long entries survive a prune. It outlives every window the
// rules read (QuietLookback, UndoWindow) by a wide margin.
const keep = 90 * 24 * time.Hour

// Ledger is the JSONL file.
type Ledger struct {
	Path string
}

// Default is the ledger under the operator's home directory.
func Default() (Ledger, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Ledger{}, err
	}
	return Ledger{Path: filepath.Join(home, ".tokenops", "coach", "followthrough.jsonl")}, nil
}

// Append writes entries, pruning old ones when the file has grown large.
func (l Ledger) Append(entries ...ft.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.Path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	unlock, err := filelock.Lock(f)
	if err != nil {
		return err
	}
	defer unlock()
	var buf bytes.Buffer
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		return err
	}
	if st, err := f.Stat(); err == nil && st.Size() > pruneAbove {
		return l.prune(f)
	}
	return nil
}

// prune rewrites the locked file in place with only recent entries. It
// truncates rather than renaming so the lock other writers wait on stays
// on the same file.
func (l Ledger) prune(f *os.File) error {
	entries, err := decode(l.Path)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, e := range ft.Prune(entries, time.Now(), keep) {
		b, _ := json.Marshal(e)
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err = f.Write(buf.Bytes())
	return err
}

// Load reads every entry. A missing file is an empty ledger; a line that
// does not parse is skipped, so one torn write cannot hide the rest.
func (l Ledger) Load() ([]ft.Entry, error) {
	entries, err := decode(l.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return entries, err
}

func decode(path string) ([]ft.Entry, error) {
	f, err := os.Open(path) //nolint:gosec // the operator's own ledger
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []ft.Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var e ft.Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Type != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}
