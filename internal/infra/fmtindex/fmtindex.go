// Package fmtindex is the infrastructure adapter for the `tokenops fmt`
// learning index — the append-only JSONL of compression + re-access records
// under the recovery directory. It is the single reader/writer shared by the
// CLI (which appends records and runs `fmt learn`) and the MCP tool surface
// (which exposes the learn report to agents), so both see the same data.
//
// The analysis itself lives in the pure domain package fmtlearn; this
// package only does file I/O.
package fmtindex

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/fmtlearn"
)

// Path returns the index file for recoverDir, defaulting to
// ~/.tokenops/recovery/index.jsonl when recoverDir is empty.
func Path(recoverDir string) (string, error) {
	if recoverDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		recoverDir = filepath.Join(home, ".tokenops", "recovery")
	}
	return filepath.Join(recoverDir, "index.jsonl"), nil
}

// Perms the index is kept at. The index records which commands ran and
// which of their output was fetched back, so it is a log of the
// operator's shell activity and belongs to them alone. It shares a
// directory with the recovery files, which hold the output itself.
const (
	filePerm os.FileMode = 0o600
	dirPerm  os.FileMode = 0o700
)

// Append writes one record to the index, creating the directory and file as
// needed.
//
// It tightens the mode of a directory or index it finds rather than only
// the ones it creates: both were 0755/0644 before this, so an upgrade has
// to repair the store it inherits.
func Append(recoverDir string, rec fmtlearn.Record) error {
	path, err := Path(recoverDir)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	if err := os.Chmod(dir, dirPerm); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm)
	if err != nil {
		return err
	}
	if err := f.Chmod(filePerm); err != nil {
		_ = f.Close()
		return err
	}
	defer func() { _ = f.Close() }()
	return json.NewEncoder(f).Encode(rec)
}

// Read returns every record in the index. A missing index is not an error —
// it yields an empty slice. Malformed rows are skipped rather than aborting.
func Read(recoverDir string) ([]fmtlearn.Record, error) {
	path, err := Path(recoverDir)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var recs []fmtlearn.Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r fmtlearn.Record
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		recs = append(recs, r)
	}
	return recs, sc.Err()
}
