// Package savingsledger stores the realized-savings ledger (#656) as JSON
// lines under ~/.tokenops/coach. Hooks append to it from many sessions at
// once, so every write takes an exclusive lock on the file.
package savingsledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/learning"
	"go.klarlabs.de/tokenops/internal/infra/filelock"
)

// pruneAbove is the size past which an append also drops old entries.
const pruneAbove = 2 << 20

// keep is how long entries survive a prune: long enough for a 10%
// holdout to gather a comparison on a light user's machine.
const keep = 180 * 24 * time.Hour

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
	return Ledger{Path: filepath.Join(home, ".tokenops", "coach", "savings.jsonl")}, nil
}

// Append writes entries, pruning old ones when the file has grown large.
func (l Ledger) Append(entries ...learning.SavingsEntry) error {
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

// prune rewrites the locked file in place with only recent entries,
// truncating rather than renaming so the lock stays on the same file.
func (l Ledger) prune(f *os.File) error {
	entries, err := decode(l.Path)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-keep)
	var buf bytes.Buffer
	for _, e := range entries {
		if e.At.Before(cutoff) {
			continue
		}
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
func (l Ledger) Load() ([]learning.SavingsEntry, error) {
	entries, err := decode(l.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return entries, err
}

func decode(path string) ([]learning.SavingsEntry, error) {
	f, err := os.Open(path) //nolint:gosec // the operator's own ledger
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []learning.SavingsEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var e learning.SavingsEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Type != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}
