// Package planhistory stores the plan history as JSON lines under
// ~/.tokenops. Plan changes are rare and made by hand, so the file stays
// small and is never pruned: a report over any past period needs every
// change that falls in it.
package planhistory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// File is the JSONL history.
type File struct {
	Path string
}

// Default is the history under the operator's home directory.
func Default() (File, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return File{}, err
	}
	return File{Path: filepath.Join(home, ".tokenops", "plan-history.jsonl")}, nil
}

// Load reads every recorded change. A missing file is an empty history.
func (f File) Load() (plans.History, error) {
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var h plans.History
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var b plans.Binding
		// A line that does not parse is skipped rather than failing every
		// report that reads the history.
		if json.Unmarshal(sc.Bytes(), &b) == nil && b.Provider != "" {
			h = append(h, b)
		}
	}
	return h, sc.Err()
}

// Append records changes.
func (f File) Append(bs ...plans.Binding) error {
	if len(bs) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	fh, err := os.OpenFile(f.Path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = fh.Close() }()
	if err := syscall.Flock(int(fh.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(fh.Fd()), syscall.LOCK_UN) }()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, b := range bs {
		if err := enc.Encode(b); err != nil {
			return err
		}
	}
	_, err = fh.Write(buf.Bytes())
	return err
}
