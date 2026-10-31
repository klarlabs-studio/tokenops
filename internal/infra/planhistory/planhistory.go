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
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"go.klarlabs.de/tokenops/internal/infra/filelock"

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

// LoadDefault reads the history under the operator's home directory.
func LoadDefault() (plans.History, error) {
	f, err := Default()
	if err != nil {
		return nil, err
	}
	return f.Load()
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
			b.Plan, _ = plans.ResolveAlias(b.Plan)
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
	unlock, err := filelock.Lock(fh)
	if err != nil {
		return err
	}
	defer unlock()
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

// RenameRetiredPlans rewrites the recorded changes to a plan by a name the
// catalog retired under the plan's current name, and says how many it
// rewrote. A line that does not parse, and every field but the plan, is
// kept as written.
func (f File) RenameRetiredPlans() (int, error) {
	fh, err := os.OpenFile(f.Path, os.O_RDWR, 0o600)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer func() { _ = fh.Close() }()
	unlock, err := filelock.Lock(fh)
	if err != nil {
		return 0, err
	}
	defer unlock()
	data, err := io.ReadAll(fh)
	if err != nil {
		return 0, err
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	renamed := 0
	for i, line := range lines {
		var rec map[string]json.RawMessage
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		var name string
		if json.Unmarshal(rec["plan"], &name) != nil {
			continue
		}
		current, retired := plans.ResolveAlias(name)
		if !retired {
			continue
		}
		rec["plan"], _ = json.Marshal(current)
		b, err := json.Marshal(rec)
		if err != nil {
			return 0, err
		}
		if bytes.HasSuffix(line, []byte("\n")) {
			b = append(b, '\n')
		}
		lines[i] = b
		renamed++
	}
	if renamed == 0 {
		return 0, nil
	}
	if err := fh.Truncate(0); err != nil {
		return 0, err
	}
	if _, err := fh.WriteAt(bytes.Join(lines, nil), 0); err != nil {
		return 0, err
	}
	return renamed, nil
}
